package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pihole-docker-dns/config"
	"pihole-docker-dns/docker"
	"pihole-docker-dns/dnswriter"
	"pihole-docker-dns/reload"
)

var (
	flConfig    = flag.String("config", "config.yaml", "config file path")
	flOutput   = flag.String("output", "", "output dnsmasq file")
	flHostIP   = flag.String("host-ip", "", "host LAN IP address")
	flDocker   = flag.String("docker", "", "docker host")
	flPoll     = flag.Int("poll", 0, "poll interval in seconds")
	flLabel    = flag.String("label", "", "container label to use for DNS")
	flReload   = flag.String("reload", "", "reload method: none, sighup, api, reconfig")
	flPihole   = flag.String("pihole", "", "Pi-hole host URL")
	flContainer = flag.String("container", "pihole", "Pi-hole container name")
	flApiURL   = flag.String("api-url", "", "Pi-hole API URL")
	flApiUser  = flag.String("api-user", "", "Pi-hole API user")
	flApiPass  = flag.String("api-pass", "", "Pi-hole API password")
	flAuthMth  = flag.String("auth-method", "", "API auth method: session, basic")
	flTLD      = flag.String("tld", "", "traefik TLD to filter hosts (e.g., '.internal')")
	flVerbose  = flag.Bool("v", false, "verbose output")
	flTestAPI  = flag.Bool("test-api", false, "test API reload and exit")
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flag.Parse()

	cfg := &config.Config{}
	if err := cfg.Load(*flConfig); err != nil {
		log.Printf("config: %v", err)
	}

	if *flOutput != "" {
		cfg.OutputFile = *flOutput
	}
	if *flHostIP != "" {
		cfg.HostIP = *flHostIP
	}
	if *flDocker != "" {
		cfg.DockerHost = *flDocker
	}
	if *flPoll > 0 {
		cfg.PollInterval = *flPoll
	}
	if *flReload != "" {
		cfg.ReloadMethod = *flReload
	}
	if *flPihole != "" {
		cfg.PiholeHost = *flPihole
	}
	if *flApiURL != "" {
		cfg.ApiURL = *flApiURL
	}
	if *flApiUser != "" {
		cfg.ApiUser = *flApiUser
	}
	if *flApiPass != "" {
		cfg.ApiPassword = *flApiPass
	}
	if *flTLD != "" {
		cfg.TLD = *flTLD
	}
	if *flAuthMth != "" {
		cfg.ApiAuthMethod = *flAuthMth
	}

	cfg.SetDefaults()

	reloader := reload.New(cfg.ReloadMethod, cfg.PiholeHost, cfg.ApiURL, cfg.ApiUser, cfg.ApiPassword, *flContainer)
	reloader.SetAuthMethod(cfg.ApiAuthMethod)
	reloader.SetDebug(*flVerbose)

	if *flVerbose {
		log.Printf("reload method: %s", reloader.Method())
		log.Printf("auth method: %s", cfg.ApiAuthMethod)
	}

	if *flTestAPI {
		if err := reloader.Reload(); err != nil {
			log.Fatalf("API reload failed: %v", err)
		}
		log.Println("API reload successful")
		return
	}

	hostIP, err := cfg.GetHostIP()
	if err != nil {
		log.Fatalf("failed to get host IP: %v", err)
	}
	if *flVerbose {
		log.Printf("using host IP: %s", hostIP)
		log.Printf("output file: %s", cfg.OutputFile)
		log.Printf("docker host: %s", cfg.DockerHost)
	}

	dockerClient, err := docker.New(ctx, cfg.DockerHost)
	if err != nil {
		log.Fatalf("failed to connect to docker: %v", err)
	}
	defer dockerClient.Close()

	if err := dockerClient.Ping(ctx); err != nil {
		log.Fatalf("failed to ping docker: %v", err)
	}
	if *flVerbose {
		log.Println("connected to docker")
	}

	writer := dnswriter.New(cfg.OutputFile)

	useTraefik := cfg.TLD != ""
	if useTraefik {
		if *flVerbose {
			log.Printf("using traefik mode with TLD: %s", cfg.TLD)
		}
		if err := updateDNSTraefik(dockerClient, writer, reloader, hostIP, cfg.TLD, ctx); err != nil {
			log.Fatalf("initial update failed: %v", err)
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			<-sigCh
			cancel()
		}()

		if err := dockerClient.MonitorEvents(ctx, "", func(containers []docker.Container) {
			if *flVerbose {
				log.Printf("event received, updating DNS")
			}
			if err := updateDNSTraefik(dockerClient, writer, reloader, hostIP, cfg.TLD, ctx); err != nil {
				log.Printf("update failed: %v", err)
			}
		}); err != nil {
			log.Printf("event monitoring failed: %v, using poll fallback", err)
			pollLoopTraefik(ctx, dockerClient, writer, reloader, hostIP, cfg.TLD, cfg.PollInterval)
		}
	} else {
		label := *flLabel
		if label == "" {
			label = "dns"
		}
		if err := updateDNS(dockerClient, writer, reloader, hostIP, label, ctx); err != nil {
			log.Fatalf("initial update failed: %v", err)
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			<-sigCh
			cancel()
		}()

		if err := dockerClient.MonitorEvents(ctx, label, func(containers []docker.Container) {
			if *flVerbose {
				log.Printf("event received, updating DNS")
			}
			if err := updateDNS(dockerClient, writer, reloader, hostIP, label, ctx); err != nil {
				log.Printf("update failed: %v", err)
			}
		}); err != nil {
			log.Printf("event monitoring failed: %v, using poll fallback", err)
			pollLoop(ctx, dockerClient, writer, reloader, hostIP, label, cfg.PollInterval)
		}
	}

	<-ctx.Done()
	log.Println("shutting down")
}

func updateDNS(d *docker.DockerClient, w *dnswriter.DNSWriter, r *reload.Reloader, hostIP, label string, ctx context.Context) error {
	containers, err := d.ListContainersWithLabel(ctx, label)
	if err != nil {
		return err
	}
	if *flVerbose {
		log.Printf("found %d containers with label %s", len(containers), label)
	}
	writerContainers := make([]dnswriter.Container, len(containers))
	for i, c := range containers {
		writerContainers[i] = dnswriter.Container{
			ID:     c.ID,
			Name:   c.Name,
			Labels: c.Labels,
			State:  c.State,
		}
	}
	if err := w.Write(writerContainers, hostIP); err != nil {
		return err
	}
	if err := r.Reload(); err != nil {
		if *flVerbose {
			log.Printf("reload failed: %v", err)
		}
	}
	return nil
}

func pollLoop(ctx context.Context, d *docker.DockerClient, w *dnswriter.DNSWriter, r *reload.Reloader, hostIP, label string, interval int) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := updateDNS(d, w, r, hostIP, label, ctx); err != nil {
				log.Printf("poll update failed: %v", err)
			}
		}
	}
}

func updateDNSTraefik(d *docker.DockerClient, w *dnswriter.DNSWriter, r *reload.Reloader, hostIP, tld string, ctx context.Context) error {
	containers, err := d.ListContainersWithTraefikHost(ctx, tld)
	if err != nil {
		return err
	}
	if *flVerbose {
		log.Printf("found %d hosts with TLD %s", len(containers), tld)
	}
	writerContainers := make([]dnswriter.Container, len(containers))
	for i, c := range containers {
		writerContainers[i] = dnswriter.Container{
			ID:     c.ID,
			Name:   c.Name,
			Labels: c.Labels,
			State:  c.State,
		}
	}
	if err := w.Write(writerContainers, hostIP); err != nil {
		return err
	}
	if err := r.Reload(); err != nil {
		if *flVerbose {
			log.Printf("reload failed: %v", err)
		}
	}
	return nil
}

func pollLoopTraefik(ctx context.Context, d *docker.DockerClient, w *dnswriter.DNSWriter, r *reload.Reloader, hostIP, tld string, interval int) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := updateDNSTraefik(d, w, r, hostIP, tld, ctx); err != nil {
				log.Printf("poll update failed: %v", err)
			}
		}
	}
}