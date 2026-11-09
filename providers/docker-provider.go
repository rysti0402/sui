package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// //----- Constants --->
const (
	// requests use unversioned API paths, so the daemon answers with its own
	// current API version and no client/daemon version pinning can go stale
	dockerRequestTimeout = 10 * time.Second
	dockerCacheTTL       = 5 * time.Second
)

var (
	dockerEnabledLabel string = fmt.Sprintf("%s.enabled", labelRoot)
	dockerNameLabel    string = fmt.Sprintf("%s.name", labelRoot)
	dockerURLLabel     string = fmt.Sprintf("%s.url", labelRoot)
	dockerIconLabel    string = fmt.Sprintf("%s.icon", labelRoot)
)

////----- end

// //----- Models --->
type Docker struct {
	Host       string
	Client     *http.Client
	BaseURL    string
	User       string
	Pass       string
	DefaultEnb bool
	Swarm      bool

	cacheMu    sync.Mutex
	cacheTime  time.Time
	containers []dockerContainer
	services   []dockerService
}
type DockerConfig struct {
	ConnType   string `json:"connection"`
	ConnPath   string `json:"path"`
	ConnURL    string `json:"url"`
	User       string `json:"user"`
	Pass       string `json:"pass"`
	DefaultEnb bool   `json:"default"`
	Swarm      bool   `json:"swarm"`
}
type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}
type dockerService struct {
	Spec struct {
		Name         string `json:"Name"`
		TaskTemplate struct {
			ContainerSpec struct {
				Labels map[string]string `json:"Labels"`
			} `json:"ContainerSpec"`
		} `json:"TaskTemplate"`
	} `json:"Spec"`
}
type dockerVersion struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
}

////----- end

// //----- Common App Provider Functions --->
func newDocker(name string) (*Docker, error) {
	log.Debugf("creating new docker provider")
	config, err := loadDockerConfig(name)
	if err != nil {
		return nil, err
	}
	docker := Docker{
		Host:       config.ConnURL,
		User:       config.User,
		Pass:       config.Pass,
		Swarm:      config.Swarm,
		DefaultEnb: config.DefaultEnb,
	}
	docker.Client, docker.BaseURL, err = config.createClient()
	if err != nil {
		return nil, err
	}
	if !docker.TestConnection(true) {
		return nil, fmt.Errorf("could not create docker connection | %s", name)
	}
	return &docker, nil
}

func toDocker(cnf interface{}) (*Docker, error) {
	var docker *Docker
	docker, valid := cnf.(*Docker)
	if !valid {
		return nil, fmt.Errorf("conversion to docker type not valid")
	}
	return docker, nil
}

// GetApps fetches all the containers visible fromthe provided Docker client
// It will however not provide any apps back where the enabled flag was false
// Returns a map of App formatted containers
func (dkr *Docker) GetApps() map[string]*App {

	if dkr.Swarm {
		return dkr.swarmGetApps()
	}
	return dkr.dockerGetApps()

}

// UpgradeApp takes an already existing app and replaces data with defined data
// an example use case is where you want to overwrite an apps info with the info
// from docker labels
// In this case (docker), match name should be the name of the container you want
// to use
// Returns true if returning a new suggested app name
func (dkr *Docker) UpgradeApp(matchName string, app *App) (string, bool) {

	if dkr.Swarm {
		return dkr.swarmUpgradeApp(matchName, app)
	}
	return dkr.dockerUpgradeApp(matchName, app)

}

// TestConnection checks to see if the docker client can be communicated with via
// the given http client. This is done by checking the version
// Returns true if communication was possible
func (dkr *Docker) TestConnection(output bool) bool {
	version, err := dkr.getDockerVersion()
	if err != nil {
		log.Errorf("docker connection test failed | %v", err)
		return false
	}
	log.Debugf("docker version checked | %s", version)
	return true
}

func loadDockerConfig(name string) (*DockerConfig, error) {
	configPath := fmt.Sprintf("%s/%s.json", getFileConfigRoot(), name)
	dkrCnf := newDockerConfig()
	cnfFile, err := os.Open(configPath)
	if err != nil {
		log.Errorf("no config file found | %s", configPath)
		return nil, err
	}
	defer cnfFile.Close()
	err = json.NewDecoder(cnfFile).Decode(dkrCnf)
	if err != nil {
		log.Errorf("config file could not be parsed | %s", configPath)
		return nil, err
	}
	return dkrCnf, nil
}

////----- end

// //----- Provider Specific ---> eof
func newDockerConfig() *DockerConfig {
	return &DockerConfig{
		ConnPath: "/var/run/docker.sock",
		ConnType: "socker",
		ConnURL:  "",
		User:     "",
		Pass:     "",
	}
}

// createClient builds one reusable http client for the docker api, either over
// the unix socket or tcp. Returns the client and the base url to request against
func (cnf *DockerConfig) createClient() (*http.Client, string, error) {
	transport := &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	switch strings.ToLower(cnf.ConnType) {
	case "unix":
		if !cnf.dockerSocketExist() {
			log.Errorf("you must mount the docker socket (%s) to use unix type", cnf.ConnPath)
			return nil, "", fmt.Errorf("docker socket is not mounted (correctly)")
		}
		socket := cnf.ConnPath
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		}
		return &http.Client{Transport: transport, Timeout: dockerRequestTimeout}, "http://docker", nil
	case "tcp":
		if !cnf.dockerTCPOkay() {
			log.Errorf("you must enter a valid [host]:[port] to use tcp type docker")
			return nil, "", fmt.Errorf("docker host is not valid")
		}
		return &http.Client{Transport: transport, Timeout: dockerRequestTimeout}, "http://" + cnf.ConnURL, nil
	default:
		return nil, "", fmt.Errorf("type must be unix or tcp | given -> %s", cnf.ConnType)
	}
}

// request performs a GET against the docker api and decodes the json response
// into out. The response body is always drained and closed so connections are
// reused instead of leaked
func (dkr *Docker) request(path string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, dkr.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if dkr.User != "" {
		req.SetBasicAuth(dkr.User, dkr.Pass)
	}
	resp, err := dkr.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker api %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	err = json.NewDecoder(resp.Body).Decode(out)
	io.Copy(io.Discard, resp.Body)
	return err
}

// refreshCache fetches containers (or services) at most once per dockerCacheTTL,
// so the traefik provider can match many routers without one api call each
func (dkr *Docker) refreshCache() error {
	dkr.cacheMu.Lock()
	defer dkr.cacheMu.Unlock()
	if time.Since(dkr.cacheTime) < dockerCacheTTL {
		return nil
	}
	if dkr.Swarm {
		var services []dockerService
		if err := dkr.request("/services", &services); err != nil {
			return err
		}
		dkr.services = services
	} else {
		var containers []dockerContainer
		if err := dkr.request("/containers/json", &containers); err != nil {
			return err
		}
		dkr.containers = containers
	}
	dkr.cacheTime = time.Now()
	return nil
}

func (dkr *Docker) dockerGetApps() map[string]*App {
	containers, err := dkr.getContainerList()
	if err != nil {
		return nil
	}
	apps := make(map[string]*App)
	for _, container := range containers {
		app := newApp()
		app.Enabled = dkr.DefaultEnb
		name := containerName(container)
		newName, upName := dkr.UpgradeApp(name, app)
		if upName {
			name = newName
		}
		if app.Enabled {
			app.formatDisplayURL(maxDisplayURLLen)
			app.formatDisplayName(name, maxDisplayNameLen)
			apps[name] = app
		}
	}

	return apps
}

func (dkr *Docker) swarmGetApps() map[string]*App {
	services, err := dkr.getServiceList()
	if err != nil {
		return nil
	}
	apps := make(map[string]*App)
	for _, service := range services {
		app := newApp()
		app.Enabled = dkr.DefaultEnb
		name := service.Spec.Name
		newName, upName := dkr.UpgradeApp(name, app)
		if upName {
			name = newName
		}
		if app.Enabled {
			app.formatDisplayURL(maxDisplayURLLen)
			app.formatDisplayName(name, maxDisplayNameLen)
			apps[name] = app
		}
	}
	return apps
}

func (dkr *Docker) dockerUpgradeApp(matchName string, app *App) (string, bool) {
	containers, err := dkr.getContainerList()
	if err != nil {
		return "", false
	}
	for _, info := range containers {
		if containerName(info) == matchName {
			return applyLabels(info.Labels, app)
		}
	}
	return "", false
}

func (dkr *Docker) swarmUpgradeApp(matchName string, app *App) (string, bool) {
	services, err := dkr.getServiceList()
	if err != nil {
		return "", false
	}
	for _, info := range services {
		if strings.ToLower(info.Spec.Name) == matchName {
			return applyLabels(info.Spec.TaskTemplate.ContainerSpec.Labels, app)
		}
	}
	return "", false
}

// applyLabels copies sui.* labels onto the app
// Returns the sui.name label and true if one was set
func applyLabels(labels map[string]string, app *App) (string, bool) {
	if lIcon, ok := labels[dockerIconLabel]; ok {
		app.Icon = lIcon
	}
	if lURL, ok := labels[dockerURLLabel]; ok {
		app.URL = lURL
	}
	if lEnab, ok := labels[dockerEnabledLabel]; ok {
		if lEnabB, err := strconv.ParseBool(lEnab); err == nil {
			app.Enabled = lEnabB
		}
	}
	if lName, ok := labels[dockerNameLabel]; ok {
		return lName, true
	}
	return "", false
}

func containerName(c dockerContainer) string {
	if len(c.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

func (cnf *DockerConfig) dockerSocketExist() bool {
	if _, err := os.Stat(cnf.ConnPath); err == nil {
		return true
	}
	return false
}

// dockerTCPOkay accepts host:port where host is an ip or a hostname
// (e.g. socket-proxy:2375)
func (cnf *DockerConfig) dockerTCPOkay() bool {
	host, port, err := net.SplitHostPort(cnf.ConnURL)
	if err != nil || host == "" {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p > 0 && p < 65536
}

func (dkr *Docker) getContainerList() ([]dockerContainer, error) {
	if err := dkr.refreshCache(); err != nil {
		log.Errorf("failed to fetch docker container list | %v", err)
		return nil, err
	}
	dkr.cacheMu.Lock()
	defer dkr.cacheMu.Unlock()
	return dkr.containers, nil
}

func (dkr *Docker) getServiceList() ([]dockerService, error) {
	if err := dkr.refreshCache(); err != nil {
		log.Errorf("failed to fetch docker services list | %v", err)
		return nil, err
	}
	dkr.cacheMu.Lock()
	defer dkr.cacheMu.Unlock()
	return dkr.services, nil
}

func (dkr *Docker) getDockerVersion() (string, error) {
	var versionData dockerVersion
	if err := dkr.request("/version", &versionData); err != nil {
		log.Errorf("failed to fetch docker version")
		return "", err
	}
	if versionData.Version == "" {
		return "", fmt.Errorf("docker version info not found")
	}
	return fmt.Sprintf("%s (api %s)", versionData.Version, versionData.APIVersion), nil
}
