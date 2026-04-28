package docker

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

var traefikRouterLabelRe = regexp.MustCompile(`^traefik\.http\.routers\..+\.rule$`)
var hostPatternRe = regexp.MustCompile(`Host\(`+"`"+`([^`+"`"+`]+)`+"`"+`\)`)

type Container struct {
	ID     string
	Name   string
	Labels map[string]string
	State  string
}

type DockerClient struct {
	cli *client.Client
}

func New(ctx context.Context, dockerHost string) (*DockerClient, error) {
	cli, err := client.NewClientWithOpts(
		client.WithHost(dockerHost),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &DockerClient{cli: cli}, nil
}

func (d *DockerClient) Close() error {
	return d.cli.Close()
}

func (d *DockerClient) ListContainersWithLabel(ctx context.Context, label string) ([]Container, error) {
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []Container
	for _, c := range containers {
		if c.State != "running" {
			continue
		}
		if c.Labels == nil {
			continue
		}
		if _, ok := c.Labels[label]; !ok {
			continue
		}
		name := c.Labels[label]
		if name == "" {
			name = c.Names[0]
		}
		result = append(result, Container{
			ID:     c.ID,
			Name:   name,
			Labels: c.Labels,
			State:  c.State,
		})
	}
	return result, nil
}

func (d *DockerClient) ListContainersWithTraefikHost(ctx context.Context, tld string) ([]Container, error) {
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []Container
	seen := make(map[string]bool)

	for _, c := range containers {
		if c.State != "running" {
			continue
		}
		if c.Labels == nil {
			continue
		}

		hosts := extractTraefikHosts(c.Labels, tld)
		if len(hosts) == 0 {
			continue
		}

		for _, host := range hosts {
			if seen[host] {
				continue
			}
			seen[host] = true
			result = append(result, Container{
				ID:     c.ID,
				Name:   host,
				Labels: c.Labels,
				State:  c.State,
			})
		}
	}
	return result, nil
}

func extractTraefikHosts(labels map[string]string, tld string) []string {
	var hosts []string
	seen := make(map[string]bool)

	for key, value := range labels {
		if !traefikRouterLabelRe.MatchString(key) {
			continue
		}

		matches := hostPatternRe.FindAllStringSubmatch(value, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			host := strings.TrimSpace(match[1])
			if !strings.HasSuffix(host, tld) {
				continue
			}
			if seen[host] {
				continue
			}
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func (d *DockerClient) MonitorEvents(ctx context.Context, label string, onChange func([]Container)) error {
	eventCh, errCh := d.cli.Events(ctx, events.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("type", string(events.ContainerEventType)),
		),
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-eventCh:
				if e.Type != events.ContainerEventType {
					continue
				}
				if e.Action != events.ActionStart && e.Action != events.ActionStop && e.Action != events.ActionDie {
					continue
				}
				var containers []Container
				var err error
				if label != "" {
					containers, err = d.ListContainersWithLabel(ctx, label)
				} else {
					containers, err = d.ListContainersAll(ctx)
				}
				if err != nil {
					continue
				}
				onChange(containers)
			case err := <-errCh:
				if err != nil {
					continue
				}
			}
		}
	}()
	return nil
}

func (d *DockerClient) ListContainersAll(ctx context.Context) ([]Container, error) {
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []Container
	for _, c := range containers {
		result = append(result, Container{
			ID:     c.ID,
			Name:   c.Names[0],
			Labels: c.Labels,
			State:  c.State,
		})
	}
	return result, nil
}

func (d *DockerClient) Ping(ctx context.Context) error {
	_, err := d.cli.Ping(ctx)
	return err
}

func (d *DockerClient) GetHostIP() (string, error) {
	iface, err := net.InterfaceByName("eth0")
	if err != nil {
		iface, err = net.InterfaceByName("en0")
		if err != nil {
			return "", err
		}
	}
	addrs, err := iface.Addrs()
	if err != nil || len(addrs) == 0 {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ip := ipnet.IP.To4(); ip != nil {
				if ip[0] == 192 || ip[0] == 10 || ip[0] == 172 {
					return ip.String(), nil
				}
			}
		}
	}
	return "", nil
}

func (d *DockerClient) WatchContainersHTTP(ctx context.Context, dockerHost string, onChange func()) (<-chan struct{}, error) {
	tr := &http.Transport{
		DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
	}
	httpClient := &http.Client{Transport: tr}

	req, err := http.NewRequest("GET", dockerHost+"/events?type=container", nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			select {
			case <-ctx.Done():
				resp.Body.Close()
				close(ch)
				return
			default:
				n, err := resp.Body.Read(buf)
				if n > 0 {
					ch <- struct{}{}
				}
				if err != nil {
					time.Sleep(time.Second)
				}
			}
		}
	}()
	return ch, nil
}