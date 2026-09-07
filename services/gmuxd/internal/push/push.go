// Package push persists browser Web Push subscriptions and delivers
// encrypted VAPID notifications to matching project subscribers.
package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

const fileName = "push-subscriptions.json"

// Keys are the PushSubscription encryption keys supplied by the browser.
type Keys struct {
	Auth   string `json:"auth"`
	P256dh string `json:"p256dh"`
}

// Subscription is one browser/device PushSubscription plus gmux metadata.
type Subscription struct {
	ID          string   `json:"id"`
	Endpoint    string   `json:"endpoint"`
	Keys        Keys     `json:"keys"`
	Projects    []string `json:"projects"`
	DeviceLabel string   `json:"device_label,omitempty"`
	UserAgent   string   `json:"user_agent,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// State is the persisted push-subscriptions.json shape.
type State struct {
	Version         int            `json:"version"`
	VAPIDPublicKey  string         `json:"vapid_public_key"`
	VAPIDPrivateKey string         `json:"vapid_private_key"`
	Subscriptions   []Subscription `json:"subscriptions"`
}

// Payload is encoded into the Web Push body and interpreted by sw.js.
type Payload struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Tag       string `json:"tag,omitempty"`
	URL       string `json:"url,omitempty"`
}

// Manager owns the persisted state file.
type Manager struct {
	mu   sync.Mutex
	path string
}

// Open loads push state, generating VAPID keys if needed.
func Open(stateDir string) (*Manager, error) {
	m := &Manager{path: filepath.Join(stateDir, fileName)}
	if _, err := m.loadOrInit(); err != nil {
		return nil, err
	}
	return m, nil
}

// PublicKey returns the VAPID public key browsers use with PushManager.subscribe.
func (m *Manager) PublicKey() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.loadOrInit()
	if err != nil {
		return "", err
	}
	return st.VAPIDPublicKey, nil
}

// Upsert stores or refreshes a browser subscription and its project filters.
func (m *Manager) Upsert(sub Subscription) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.TrimSpace(sub.Endpoint) == "" || sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
		return Subscription{}, fmt.Errorf("push: endpoint and keys are required")
	}

	st, err := m.loadOrInit()
	if err != nil {
		return Subscription{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	sub.ID = idForEndpoint(sub.Endpoint)
	sub.Projects = normalizeProjects(sub.Projects)
	sub.UpdatedAt = now

	for i := range st.Subscriptions {
		if st.Subscriptions[i].Endpoint != sub.Endpoint {
			continue
		}
		sub.CreatedAt = st.Subscriptions[i].CreatedAt
		if sub.CreatedAt == "" {
			sub.CreatedAt = now
		}
		st.Subscriptions[i] = sub
		if err := m.save(st); err != nil {
			return Subscription{}, err
		}
		return sub, nil
	}

	sub.CreatedAt = now
	st.Subscriptions = append(st.Subscriptions, sub)
	if err := m.save(st); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

// Lookup returns a subscription by endpoint.
func (m *Manager) Lookup(endpoint string) (Subscription, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.loadOrInit()
	if err != nil {
		return Subscription{}, false, err
	}
	for _, sub := range st.Subscriptions {
		if sub.Endpoint == endpoint {
			return sub, true, nil
		}
	}
	return Subscription{}, false, nil
}

// UpdateProjects replaces the per-project filter for an existing subscription.
func (m *Manager) UpdateProjects(endpoint string, projectSlugs []string) (Subscription, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.loadOrInit()
	if err != nil {
		return Subscription{}, false, err
	}
	for i := range st.Subscriptions {
		if st.Subscriptions[i].Endpoint != endpoint {
			continue
		}
		st.Subscriptions[i].Projects = normalizeProjects(projectSlugs)
		st.Subscriptions[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := m.save(st); err != nil {
			return Subscription{}, false, err
		}
		return st.Subscriptions[i], true, nil
	}
	return Subscription{}, false, nil
}

// Delete removes a subscription by endpoint.
func (m *Manager) Delete(endpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.loadOrInit()
	if err != nil {
		return err
	}
	filtered := st.Subscriptions[:0]
	for _, sub := range st.Subscriptions {
		if sub.Endpoint != endpoint {
			filtered = append(filtered, sub)
		}
	}
	st.Subscriptions = filtered
	return m.save(st)
}

// Matching returns a snapshot of subscriptions that opted into projectSlug.
func (m *Manager) Matching(projectSlug string) ([]Subscription, error) {
	projectSlug = strings.TrimSpace(projectSlug)
	if projectSlug == "" {
		return nil, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.loadOrInit()
	if err != nil {
		return nil, err
	}
	var out []Subscription
	for _, sub := range st.Subscriptions {
		if contains(sub.Projects, projectSlug) {
			out = append(out, sub)
		}
	}
	return out, nil
}

// Send delivers payload to every subscription opted into projectSlug.
func (m *Manager) Send(ctx context.Context, projectSlug string, payload []byte) {
	targets, err := m.Matching(projectSlug)
	if err != nil {
		log.Printf("push: matching subscriptions: %v", err)
		return
	}
	if len(targets) == 0 {
		return
	}

	publicKey, privateKey, err := m.keys()
	if err != nil {
		log.Printf("push: loading VAPID keys: %v", err)
		return
	}

	for _, sub := range targets {
		go m.sendOne(ctx, publicKey, privateKey, sub, payload)
	}
}

func (m *Manager) sendOne(ctx context.Context, publicKey, privateKey string, sub Subscription, payload []byte) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			Auth:   sub.Keys.Auth,
			P256dh: sub.Keys.P256dh,
		},
	}, &webpush.Options{
		Subscriber:      "mailto:gmux@localhost",
		TTL:             60 * 60,
		Topic:           "gmux",
		Urgency:         webpush.UrgencyNormal,
		VAPIDPublicKey:  publicKey,
		VAPIDPrivateKey: privateKey,
	})
	if err != nil {
		log.Printf("push: send to %s: %v", sub.ID, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
		log.Printf("push: subscription %s expired (%d), removing", sub.ID, resp.StatusCode)
		if err := m.Delete(sub.Endpoint); err != nil {
			log.Printf("push: remove expired subscription: %v", err)
		}
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("push: send to %s returned HTTP %d", sub.ID, resp.StatusCode)
	}
}

func (m *Manager) keys() (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.loadOrInit()
	if err != nil {
		return "", "", err
	}
	return st.VAPIDPublicKey, st.VAPIDPrivateKey, nil
}

func (m *Manager) loadOrInit() (*State, error) {
	st, err := load(m.path)
	if err != nil {
		return nil, err
	}
	changed := false
	if st.Version == 0 {
		st.Version = 1
		changed = true
	}
	if st.VAPIDPublicKey == "" || st.VAPIDPrivateKey == "" {
		privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, fmt.Errorf("push: generating VAPID keys: %w", err)
		}
		st.VAPIDPublicKey = publicKey
		st.VAPIDPrivateKey = privateKey
		changed = true
	}
	if changed {
		if err := m.save(st); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Version: 1}, nil
		}
		return nil, fmt.Errorf("push: reading %s: %w", path, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("push: parsing %s: %w", path, err)
	}
	return &st, nil
}

func (m *Manager) save(st *State) error {
	st.Version = 1
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return fmt.Errorf("push: creating state dir: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("push: marshaling: %w", err)
	}
	data = append(data, '\n')
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("push: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("push: renaming %s -> %s: %w", tmp, m.path, err)
	}
	return nil
}

func idForEndpoint(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:8])
}

func normalizeProjects(projects []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range projects {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
