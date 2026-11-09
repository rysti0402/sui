> **Maintained fork.** Fixes a file-descriptor leak that made the original stop answering after a few hours ("too many open files"), drops the pinned 2019 Docker client so it works with current Docker (29+), accepts hostnames such as `socket-proxy:2375` for tcp Docker connections, and publishes `ghcr.io/rysti0402/sui`.

## SUI
*a startpage for your server and / or new tab page*

> This fork provides mechanisms for self-population: using Docker or Træfik as a source

![screenshot](https://i.imgur.com/J4d7Q3D.png)

[More screenshots](https://imgur.com/a/FDVRIyw)

### Deploy with Docker compose

#### Prerequisites:
 - Docker: [Linux](https://docs.docker.com/install/linux/docker-ce/debian/), [Mac](https://hub.docker.com/editions/community/docker-ce-desktop-mac), [Windows](https://hub.docker.com/editions/community/docker-ce-desktop-windows)
 - [Docker-compose](https://docs.docker.com/compose/install/) 

  - The Docker images are available on Docker Hub and the GitHub container registry
    - `ghcr.io/willfantom/sui:latest` or `docker.io/willfantom/sui:latest`

  - Images are available for `arm` too for deployment on raspberry pi. 

### Shameless Plug

This was designed to work with my compose setup. This setup can be found [here](https://github.com/willfantom/composing). This is a good reference if you want too see how the configuration works using both docker labels and the traefik API.

### Configuration

This version of SUI is designed to pull the apps list from an external provider.

There are 2 layers of configuration for this dashboard, static and dynamic. Static sets up connections to services such as Docker or Traefik, whereas dynamic uses features such as docker labels to overwrite data received from the App Providers.

Currently supported providers are:
 - `Docker` via socket (`docker`)
 - `Træfik` via API (`traefik`)

A guide on static config - [here](./examples/guide.md)

#### Dynamic Config

##### Using Docker Labels

If you have added a Docker provider (either by itself or with a traefik provider), you can use docker labels to overwrite values.

for example:
 - `sui.enabled=true` will hide the service from the dashboard if false
 - `sui.name=Example` will set the application's name to `Example` rather than the containers name
 - `sui.url=https://a.example.tld` will link the application to the given value rather than the traefik router url
 - `sui.icon=application` will set the apps icon to `application` (see [here](https://materialdesignicons.com/))

### Customization

#### Changing color themes
 - Click the options button on the left bottom

#### Color themes
These can be added or customized in the themer.js file. When changing the name of a theme or adding one, make sure to edit this section in index.html accordingly:

```
    <section  class="themes">
```

I might add a simpler way to edit themes at some point, but adding the current ones should be pretty straight forward.

### TODO

- [ ] Ignore traefik service with a regex
- [ ] Connect to remote docker instances via TCP
  - [ ] Ensure auth works with this too
- [ ] If service goes missing, perhaps gray out for a few refresh cycles
- [ ] Password protect `protected` services
- [ ] Add some other providers (including a simple File)
- [ ] Add more default icons