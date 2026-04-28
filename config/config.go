package config

import (
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	OutputFile    string `yaml:"output_file"`
	HostIP        string `yaml:"host_ip"`
	DockerHost    string `yaml:"docker_host"`
	PollInterval  int    `yaml:"poll_interval"`
	ReloadMethod  string `yaml:"reload_method"`
	PiholeHost     string `yaml:"pihole_host"`
	ApiURL         string `yaml:"api_url"`
	ApiUser        string `yaml:"api_user"`
	ApiPassword   string `yaml:"api_password"`
	ApiAuthMethod string `yaml:"api_auth_method"` // basic or session
	TLD           string `yaml:"tld"`
}

func (c *Config) SetDefaults() {
	if c.OutputFile == "" {
		c.OutputFile = "dnsmasq-docker.conf"
	}
	if c.PollInterval == 0 {
		c.PollInterval = 60
	}
	if c.DockerHost == "" {
		c.DockerHost = "unix:///var/run/docker.sock"
	}
	if c.ReloadMethod == "" {
		c.ReloadMethod = "none"
	}
	if c.PiholeHost == "" {
		c.PiholeHost = "http://localhost"
	}
	if c.PiholeHost == "http://localhost" && os.Getenv("PIHOLE_GRAPH") != "" {
		c.PiholeHost = os.Getenv("PIHOLE_GRAPH")
	}
	if c.ApiAuthMethod == "" {
		c.ApiAuthMethod = "session"
	}
}

func (c *Config) ApplyEnv() {
	if v := os.Getenv("PDNS_OUTPUT_FILE"); v != "" {
		c.OutputFile = v
	}
	if v := os.Getenv("PDNS_HOST_IP"); v != "" {
		c.HostIP = v
	}
	if v := os.Getenv("PDNS_DOCKER_HOST"); v != "" {
		c.DockerHost = v
	}
	if v := os.Getenv("PDNS_POLL_INTERVAL"); v != "" {
		fmt.Sscanf(v, "%d", &c.PollInterval)
	}
	if v := os.Getenv("PDNS_RELOAD_METHOD"); v != "" {
		c.ReloadMethod = v
	}
	if v := os.Getenv("PIHOLE_WEB"); v != "" {
		c.PiholeHost = v
	}
	if v := os.Getenv("PIHOLE_API_KEY"); v != "" {
		c.ApiPassword = v
	}
	if v := os.Getenv("PIHOLE_AUTH_METHOD"); v != "" {
		c.ApiAuthMethod = v
	}
	if v := os.Getenv("PDNS_TLD"); v != "" {
		c.TLD = v
	}
}

func (c *Config) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return err
	}
	c.SetDefaults()
	c.ApplyEnv()
	return nil
}

func (c *Config) GetHostIP() (string, error) {
	if c.HostIP != "" {
		return c.HostIP, nil
	}
	return detectHostIP()
}

func detectHostIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.To4() == nil {
				continue
			}
			if ip[0] == 10 || ip[0] == 192 || ip[0] == 172 {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no suitable host IP found")
}