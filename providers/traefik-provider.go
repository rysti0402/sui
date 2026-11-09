package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

////----- Constants --->
const (
	traefikRoutersPath string = "api/http/routers"
)

////----- end

////----- Models --->
type Traefik struct {
	URL     string
	User    string
	Pass    string
	Dockers map[string]*Docker
	Ignore  []string
}
type TraefikConfig struct {
	URL     string   `json:"url"`
	Dockers []string `json:"dockers"`
	Ignored []string `json:"ignored"`
	User    string   `json:"user"`
	Pass    string   `json:"pass"`
}
type TraefikRouter struct {
	Name    string `json:"service"`
	RuleStr string `json:"rule"`
	Status  string `json:"status"`
	Enabled bool
}
type TraefikVersion struct {
	Version string `json:"Version"`
	Codename string `json:"Codename"`
}
////----- end


////----- Common App Provider Functions --->
func newTraefik(name string) (*Traefik, error) {
	log.Debugf("creating new traefik provider")
	config, err := loadTraefikConfig(name)
	if err != nil {
		return nil, err
	}
	traefik := Traefik{
		User: config.User,
		Pass: config.Pass,
		URL: config.URL,
		Ignore: config.Ignored,
		Dockers: make(map[string]*Docker),
	}
	if !traefik.TestConnection(true) {
		return nil, fmt.Errorf("could not create traefik connection | %s", name)
	}
	for _, dkr := range config.Dockers {
		traefik.Dockers[dkr], err = newDocker(dkr)
		if err != nil {
			return nil, fmt.Errorf("could not add dockers to traefik provider | %s", name)
		}
	}
	return &traefik, nil
}

func toTraefik(cnf interface{}) (*Traefik, error) {
	var traefik *Traefik
	traefik, valid := cnf.(*Traefik)
	if !valid {
		return nil, fmt.Errorf("conversion to traefik type not valid")
	}
	return traefik, nil
}

// GetApps fetches all the routers visible fromthe provided traefik instance
// It will however not provide any apps back where the enabled flag was false
// if provided with a docker connection
// Returns a map of App formatted routers
func (tr *Traefik) GetApps() map[string]*App {

	routers, err := tr.getRouterList()
	if err != nil {
		return nil
	}

	apps := make(map[string]*App)
	for _, router := range routers {
		app := newApp()
		name := router.Name
		for _, igname := range tr.Ignore {
			if strings.ToLower(name) == strings.ToLower(igname) {
				app.Enabled = false
			}
		}
		app.Icon = getDefaultIcon(name)
		if router.RuleStr != "" {
			domainStrs := parseDomains(router.RuleStr)
			if len(domainStrs) > 0 {
				app.URL = domainStrs[0]
			}
		}
		for _, dkr := range tr.Dockers {
			newName, upName := dkr.UpgradeApp(name, app)
			if upName {
				name = newName
			}
		}
		if app.Enabled {

			app.formatDisplayURL(maxDisplayURLLen)
			app.formatDisplayName(name, maxDisplayNameLen)
			
			apps[name] = app
		}
	}

	return apps
}

// TestConnection checks to see if traefik can be communicated with via
// the given url. This is done by checking the version
// Returns true if communication was possible
func (tr *Traefik) TestConnection(output bool) bool {
	version, err := tr.getTraefikVersion()
	if err != nil {
		return false
	}
	log.Debugf("traefik version checked | %s", version.Version)
	return true
}

func loadTraefikConfig(name string) (*TraefikConfig, error) {
	configPath := fmt.Sprintf("%s/%s.json", getFileConfigRoot(), name)
	trCnf := newTraefikConfig()
	cnfFile, err := os.Open(configPath)
	if err != nil {
		log.Errorf("no config file found | %s", configPath)
		return nil, err
	}
	defer cnfFile.Close()
	err = json.NewDecoder(cnfFile).Decode(trCnf)
	if err != nil {
		log.Errorf("config file could not be parsed | %s", configPath)
		return nil, err
	}
	if strings.HasSuffix(trCnf.URL, "/")  {
		trCnf.URL = trCnf.URL[:len(trCnf.URL)-1]
	}
	return trCnf, nil
}
////----- end

////----- Provider Specific ---> eof
func newTraefikConfig() *TraefikConfig {
	return &TraefikConfig{}
}

// traefikClient is shared by every request so idle connections are reused,
// and the timeout stops a stalled traefik api from hanging the refresh loop
var traefikClient = &http.Client{Timeout: 10 * time.Second}

// getJSON performs a GET against the traefik api and decodes the json response
// into out. The response body is always drained and closed so the underlying
// connection is released (the original code never closed it and leaked a file
// descriptor on every refresh)
func (tr *Traefik) getJSON(path string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/%s", tr.URL, path), nil)
	if err != nil {
		return err
	}
	if tr.User != "" {
		req.SetBasicAuth(tr.User, tr.Pass)
	}
	resp, err := traefikClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("traefik api %s returned %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (tr *Traefik) getRouterList() ([]*TraefikRouter, error) {
	var routerList []*TraefikRouter
	if err := tr.getJSON(traefikRoutersPath, &routerList); err != nil {
		log.Errorf("failed to fetch traefik router list | %v", err)
		return nil, err
	}
	return routerList, nil
}

func (tr *Traefik) getTraefikVersion() (*TraefikVersion, error) {
	var versionInfo TraefikVersion
	if err := tr.getJSON("api/version", &versionInfo); err != nil {
		log.Errorf("failed to fetch traefik version | %v", err)
		return nil, err
	}
	return &versionInfo, nil
}

var (
	// Host(...) but not HostRegexp(...) or HostSNI(...)
	hostMatcherRe = regexp.MustCompile(`\bHost\(([^)]*)\)`)
	// a backtick, double or single quoted argument
	hostArgRe = regexp.MustCompile("[`\"']([^`\"']+)[`\"']")
)

// parseDomains returns the domains named in Host() matchers of a traefik rule,
// for both v2 (Host(`a`, `b`)) and v3 (Host(`a`) || Host(`b`)) syntax
func parseDomains(rule string) []string {
	var domains []string
	for _, m := range hostMatcherRe.FindAllStringSubmatch(rule, -1) {
		for _, arg := range hostArgRe.FindAllStringSubmatch(m[1], -1) {
			domains = append(domains, strings.ToLower(strings.TrimSpace(arg[1])))
		}
	}
	return domains
}
