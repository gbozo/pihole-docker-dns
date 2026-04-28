package reload

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type Reloader struct {
	method          string
	authMethod      string
	piholeHost     string
	apiURL         string
	apiUser        string
	apiPassword   string
	container      string
	dockerHost    string
	sid            string
	csrf           string
	debug          bool
}

type SessionResponse struct {
	Session struct {
		Valid   bool   `json:"valid"`
		TOTP    bool   `json:"totp"`
		SID     string `json:"sid"`
		CSRF    string `json:"csrf"`
		Validty int    `json:"validity"`
	} `json:"session"`
}

func New(method, piholeHost, apiURL, apiUser, apiPassword, container string) *Reloader {
	r := &Reloader{
		method:       method,
		piholeHost:   piholeHost,
		apiURL:      apiURL,
		apiUser:     apiUser,
		apiPassword: apiPassword,
		container:   container,
		dockerHost:  "unix:///var/run/docker.sock",
	}
	if r.method == "" {
		r.method = "none"
	}
	return r
}

func (r *Reloader) SetAuthMethod(method string) {
	if method == "" {
		method = "session"
	}
	r.authMethod = method
}

func (r *Reloader) SetDebug(debug bool) {
	r.debug = debug
}

func (r *Reloader) Reload() error {
	switch r.method {
	case "sighup":
		return r.sighupReload()
	case "api":
		return r.apiReload()
	case "reconfig":
		return r.dockerReconfig()
	case "none":
		return nil
	default:
		return nil
	}
}

func (r *Reloader) sighupReload() error {
	return r.execInContainer("killall", "-HUP", "dnsmasq")
}

func (r *Reloader) dockerReconfig() error {
	return r.execInContainer("pihole", "reconfig")
}

func (r *Reloader) execInContainer(cmd ...string) error {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.WithHost(r.dockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client error: %w", err)
	}
	defer cli.Close()

	resp, err := cli.ContainerExecCreate(ctx, r.container, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("exec create error: %w", err)
	}

	err = cli.ContainerExecStart(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("exec start error: %w", err)
	}

	return nil
}

func (r *Reloader) authenticate() error {
	if r.authMethod == "basic" {
		if r.debug {
			log.Println("[reload] auth: using basic auth method, skipping session auth")
		}
		return nil
	}

	if r.apiPassword == "" {
		if r.debug {
			log.Println("[reload] auth: no password provided, skipping authentication")
		}
		return nil
	}

	authURL := r.piholeHost + "/api/auth"
	payload := fmt.Sprintf(`{"password":"%s"}`, r.apiPassword)
	
	if r.debug {
		log.Printf("[reload] auth: POST %s", authURL)
		log.Printf("[reload] auth: payload %s", payload)
		log.Printf("[reload] auth: headers Content-Type: application/json")
	}
	
	req, err := http.NewRequest("POST", authURL, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("auth request creation failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("auth request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if r.debug {
		log.Printf("[reload] auth: response status %d", resp.StatusCode)
		log.Printf("[reload] auth: response body %s", string(body))
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("auth returned status %d", resp.StatusCode)
	}

	var sessionResp SessionResponse
	if err := json.Unmarshal(body, &sessionResp); err != nil {
		return fmt.Errorf("failed to decode session response: %w", err)
	}

	r.sid = sessionResp.Session.SID
	r.csrf = sessionResp.Session.CSRF

	if r.debug {
		log.Printf("[reload] auth: received sid=%s", r.sid)
		log.Printf("[reload] auth: received csrf=%s", r.csrf)
		log.Printf("[reload] auth: session valid=%v", sessionResp.Session.Valid)
	}

	return nil
}

func (r *Reloader) apiReload() error {
	if err := r.authenticate(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	if r.authMethod == "basic" {
		return r.apiReloadBasic()
	}
	return r.apiReloadSession()
}

func (r *Reloader) apiReloadBasic() error {
	url := r.piholeHost + r.apiURL
	if url == r.piholeHost {
		url = r.piholeHost + "/admin/api.php/inform/reload"
	}

	if r.debug {
		log.Printf("[reload] api: POST %s", url)
		if r.apiUser != "" {
			log.Printf("[reload] api: using basic auth user=%s", r.apiUser)
		}
	}

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return err
	}
	if r.apiUser != "" && r.apiPassword != "" {
		req.SetBasicAuth(r.apiUser, r.apiPassword)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("api request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if r.debug {
		log.Printf("[reload] api: response status %d", resp.StatusCode)
		log.Printf("[reload] api: response body %s", string(body))
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("api returned status %d", resp.StatusCode)
	}
	return nil
}

func (r *Reloader) apiReloadSession() error {
	url := r.piholeHost + "/api/action/restartdns"

	if r.debug {
		log.Printf("[reload] api: POST %s", url)
		log.Printf("[reload] api: payload {}", "{}")
		log.Printf("[reload] api: headers Content-Type: application/json")
		log.Printf("[reload] api: X-FTL-SID %s", r.sid)
		log.Printf("[reload] api: X-FTL-CSRF %s", r.csrf)
	}

	req, err := http.NewRequest("POST", url, strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("api request creation failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-FTL-SID", r.sid)
	req.Header.Set("X-FTL-CSRF", r.csrf)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("api request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if r.debug {
		log.Printf("[reload] api: response status %d", resp.StatusCode)
		log.Printf("[reload] api: response body %s", string(body))
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("api returned status %d", resp.StatusCode)
	}
	return nil
}

func (r *Reloader) Method() string {
	return r.method
}

func (r *Reloader) ValidMethods() []string {
	return []string{"none", "sighup", "api", "reconfig"}
}

func ValidateMethod(method string) bool {
	methods := []string{"none", "sighup", "api", "reconfig"}
	method = strings.ToLower(method)
	for _, m := range methods {
		if m == method {
			return true
		}
	}
	return false
}