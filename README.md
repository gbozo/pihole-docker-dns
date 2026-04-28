# pihole-docker-dns

Monitor Docker containers and generate local DNS entries for Pi-hole.

## Overview

This program monitors running Docker containers. When a container has a `dns` label, its value is used as the hostname and an entry is added to a dnsmasq-compatible file for local DNS resolution via Pi-hole.

## Usage

```bash
# Build
go build -o pihole-docker-dns .

# Run
./pihole-docker-dns

# With custom config
./pihole-docker-dns -config /path/to/config.yaml

# With CLI overrides
./pihole-docker-dns -output /etc/pihole/dnsmasq.d/docker.conf -host-ip 192.168.1.50
```
## Example working side by side with pihole, pihole-docker-dns, traefik and fail2ban as containers
```yaml
---
# More info at https://github.com/pi-hole/docker-pi-hole/ and https://docs.pi-hole.net/
services:
  pihole:
    container_name: pihole
    image: pihole/pihole:latest
    network_mode: "host"
    labels:
      traefik.enable: true
      # Define the router
      traefik.http.routers.pihole.rule: Host(`pi.hole`)
      #traefik.http.routers.pihole.entrypoints: web   

      # The Magic Sauce: Explicitly define the service endpoint
      traefik.http.services.pihole.loadbalancer.server.url: http://host.docker.internal:8100
    # For DHCP it is recommended to remove these ports and instead add: network_mode: "host"
    #ports:
    #  - "53:53/tcp"
    #  - "53:53/udp"
    #   - "67:67/udp" # Only required if you are using Pi-hole as your DHCP server
    #  - "8100:8100/tcp"
    environment:
      FTLCONF_webserver_api_password: admin
      FTLCONF_webserver_port: '8100'
    # Volumes store your data between container upgrades
    volumes:
      - CHANGE_TO_COMPOSE_DATA_PATH/pihole/etc-pihole:/etc/pihole
      - CHANGE_TO_COMPOSE_DATA_PATH/pihole/etc-dnsmasq.d:/etc/dnsmasq.d
    #   https://github.com/pi-hole/docker-pi-hole#note-on-capabilities
    cap_add:
      - NET_ADMIN # Required if you are using Pi-hole as your DHCP server, else not needed
    restart: unless-stopped
    tmpfs:
      - /tmp:mode=1777
  pihole-docker-dns:
    image: docker.io/library/pihole-docker-dns:latest
    container_name: pihole-docker-dns
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - CHANGE_TO_COMPOSE_DATA_PATH/pihole/etc-dnsmasq.d:/etc/dnsmasq.d
    environment:
      - PDNS_TLD=.internal
      - PDNS_OUTPUT_FILE=/etc/dnsmasq.d/dnsmasq-docker.conf
      - PDNS_HOST_IP=###Your-host-IP###
      - PDNS_POLL_INTERVAL=60
      - PDNS_RELOAD_METHOD=api
      - PIHOLE_WEB=http://pi.hole:8100
      - PIHOLE_AUTH_METHOD=session
      - PIHOLE_API_KEY=admin
    command: -config /dev/null
    restart: unless-stopped
    network_mode: host
traefik:
    # The official v3 Traefik docker image
    image: traefik:v3.6
    extra_hosts:
      - "host.docker.internal:host-gateway"
    # Enables the web UI and tells Traefik to listen to docker
    command:
     # See https://doc.traefik.io/traefik/v2.10/observability/access-logs/
      - --accesslog=true
      - --accesslog.filepath=/logs/access.log
      - --accesslog.format=json
      - --accesslog.bufferingsize=100
      - --api.insecure=true
      - --api.dashboard=true
      - --log.level=info
      - --entrypoints.web.address=:80
      - --entrypoints.websecure.address=:443
      #- --entrypoints.web.http.redirections.entryPoint.to=websecure
      #- --entrypoints.web.http.redirections.entryPoint.scheme=https
      # See https://doc.traefik.io/traefik/v2.10/https/acme/
      # Let's Encrypt HTTP-01
      - --certificatesresolvers.letsencrypt.acme.email=you@example.com
      - --certificatesresolvers.letsencrypt.acme.storage=/letsencrypt/acme-http.json
      - --certificatesresolvers.letsencrypt.acme.httpchallenge=true
      - --certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web
      # Cloudflare DNS-01
      - --certificatesresolvers.cloudflare.acme.email=you@example.com
      - --certificatesresolvers.cloudflare.acme.storage=/letsencrypt/acme-dns.json
      - --certificatesresolvers.cloudflare.acme.dnschallenge=true
      - --certificatesresolvers.cloudflare.acme.dnschallenge.provider=cloudflare
      - --certificatesresolvers.cloudflare.acme.dnschallenge.resolvers=1.1.1.1:53,8.8.8.8:53
      # https://doc.traefik.io/traefik/v2.10/providers/docker/
      - --providers.docker
      - --providers.docker.exposedByDefault=false
      - --providers.file.directory=/etc/traefik
      - --providers.file.watch=true
    environment:
      - TZ=Europe/Athens
      - CF_DNS_API_TOKEN=---------------------------------------------------
    ports:
      # The HTTP port
      - "80:80"
      - "443:443"
      # The Web UI (enabled by --api.insecure=true)
      - "8081:8080"
    volumes:
      # So that Traefik can listen to the Docker events
      - /var/run/docker.sock:/var/run/docker.sock
      - CHANGE_TO_COMPOSE_DATA_PATH/traefik/config:/etc/traefik
      - CHANGE_TO_COMPOSE_DATA_PATH/traefik/letsencrypt:/letsencrypt
      - CHANGE_TO_COMPOSE_DATA_PATH/traefik/logs:/logs
    restart: unless-stopped
    networks:
      - proxy
  fail2ban:
    image: crazymax/fail2ban:latest
    network_mode: host
    cap_add:
      - NET_ADMIN
      - NET_RAW
    volumes:
      - CHANGE_TO_COMPOSE_DATA_PATH/traefik/logs:/var/log/traefik:ro
      - CHANGE_TO_COMPOSE_DATA_PATH/traefik/fail2ban/data:/data
      - CHANGE_TO_COMPOSE_DATA_PATH/traefik/fail2ban/config:/etc/fail2ban/filter.d
    environment:
      - TZ=Europe/Athens
      - F2B_LOG_LEVEL=INFO
      - IPTABLES_MODE=auto #nft, legacy, auto
    restart: unless-stopped
networks:
    proxy:
      name: proxy
```
Then in your regular container services f.e. Baikal:
```yaml
services:
  baikal:
    image: ckulka/baikal:nginx
    restart: unless-stopped
    labels:     
      traefik.enable: true
      traefik.http.routers.baikal.rule: Host(`baikal.internal`) || Host(`baikal`)
      #traefik.http.routers.baikal.tls: true
      # Traefik middleware required for iOS, see https://github.com/ckulka/baikal-docker/issues/37.
      traefik.http.routers.baikal.middlewares: baikal-dav
      traefik.http.middlewares.baikal-dav.redirectregex.regex: https://(.*)/.well-known/(card|cal)dav
      traefik.http.middlewares.baikal-dav.redirectregex.replacement: https://$$1/dav.php/
      traefik.http.middlewares.baikal-dav.redirectregex.permanent: true      
#    ports:
#      - "8125:80"
    volumes:
      - CHANGE_TO_COMPOSE_DATA_PATH/baikal/config:/var/www/baikal/config
      - CHANGE_TO_COMPOSE_DATA_PATH/baikal/data:/var/www/baikal/Specific    
    networks:
      - proxy
networks:
  proxy:
    external: true
```
And your baikal host will be resolvable like http://baikal.internal in your home network.

## Configuration

### config.yaml

```yaml
output_file: "./dnsmasq-docker.conf"
host_ip: ""              # auto-detect if empty
docker_host: "unix:///var/run/docker.sock"
poll_interval: 60      # seconds

# Reload configuration
reload_method: "none"  # none, sighup, api, reconfig
pihole_host: "http://localhost"
api_url: "/admin/api.php/inform/reload"
api_user: "admin"
api_password: ""
```

### Environment Variables

- `PDNS_OUTPUT_FILE` - output file path
- `PDNS_HOST_IP` - host LAN IP
- `PDNS_DOCKER_HOST` - docker host
- `PDNS_POLL_INTERVAL` - poll interval
- `PDNS_RELOAD_METHOD` - reload method
- `PIHOLE_WEB` - Pi-hole web UI URL (e.g. http://pihole.local)
- `PIHOLE_API_KEY` - Pi-hole API token

### CLI Flags

- `-config` - config file path
- `-output` - output file path
- `-host-ip` - host LAN IP
- `-docker` - docker host
- `-poll` - poll interval in seconds
- `-label` - container label to use (default: dns)
- `-reload` - reload method: none, sighup, api, reconfig
- `-pihole` - Pi-hole host URL
- `-container` - Pi-hole container name (default: pihole)
- `-api-url` - Pi-hole API URL
- `-api-user` - Pi-hole API user
- `-api-pass` - Pi-hole API password
- `-v` - verbose output

## Container Labels

Add the `dns` label to your containers:

```yaml
services:
  web:
    image: nginx
    labels:
      dns: "myapp.local"
```

## Output Format

Generates dnsmasq-compatible file:

```
# Generated by pihole-docker-dns
# Do not edit manually - this file is overwritten

address=/myapp.local/192.168.1.100
```

## Pi-hole Integration

### Reload Methods

The program supports automatic Pi-hole reload after DNS updates:

| Method | Description | Notes |
|--------|-------------|-------|
| `none` | No reload | Manual reload required |
| `sighup` | Signal dnsmasq | Uses `docker exec pihole killall -HUP dnsmasq` |
| `reconfig` | Run pihole reconfig | Uses `docker exec pihole pihole reconfig` |
| `api` | HTTP API call | Calls Pi-hole inform API |

### Setup

**Option 1: Volume Mount + SIGHUP**

```yaml
# docker-compose.yml
services:
  pihole:
    volumes:
      - ./dnsmasq-docker.conf:/etc/dnsmasq.d/docker.conf

  dns-monitor:
    image: your-built-image
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    command: -reload sighup -container pihole
    depends_on:
      - pihole
```

**Option 2: API with Authentication**

```yaml
# config.yaml
reload_method: "api"
pihole_host: "http://pihole.local"
api_user: "admin"
api_password: "your-password"
```

Or via environment:

```bash
export PIHOLE_WEB="http://pihole.local"
export PDNS_RELOAD_METHOD="api"
./pihole-docker-dns -api-user admin -api-pass $PIHOLE_API_KEY
```

## Auto-Start

### Systemd Service

```ini
[Unit]
Description=Pi-hole Docker DNS Monitor
After=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/pihole-docker-dns -config /etc/pihole-docker-dns/config.yaml
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### Docker Compose

```yaml
services:
  dns-monitor:
    build: .
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./dnsmasq-docker.conf:/etc/pihole/dnsmasq.d/docker.conf
    environment:
      - PDNS_RELOAD_METHOD=sighup
    command: -reload sighup -container pihole
    restart: unless-stopped
    depends_on:
      - pihole
```
