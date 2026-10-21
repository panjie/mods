// Package chatgpt implements Sign in with ChatGPT for local open-source clients.
package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type Account struct {
	ClientID     string    `json:"client_id"`
	Subject      string    `json:"subject"`
	Email        string    `json:"email"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	Scope        string    `json:"scope"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type State struct {
	HostID   string    `json:"ext_agent_host_id"`
	Active   string    `json:"active"`
	Accounts []Account `json:"accounts"`
}
type Store struct{ Dir string }

func DefaultStore() Store {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir, _ = os.UserConfigDir()
	}
	if dir == "" {
		return Store{}
	}
	return Store{Dir: filepath.Join(dir, "mods", "chatgpt")}
}
func (s Store) Load() (State, error) {
	var state State
	if s.Dir == "" {
		return state, errors.New("cannot determine ChatGPT credential directory")
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "accounts.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(b, &state)
	return state, err
}
func (s State) Selected() (Account, bool) {
	for _, a := range s.Accounts {
		if a.ClientID == s.Active {
			return a, true
		}
	}
	return Account{}, false
}
func (s Store) SignedIn() bool {
	st, e := s.Load()
	a, ok := st.Selected()
	return e == nil && ok && a.RefreshToken != ""
}

// update serializes credential mutations, including rotating refresh tokens, across processes.
func (s Store) update(ctx context.Context, fn func(*State) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Dir == "" {
		return errors.New("cannot determine ChatGPT credential directory")
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	lock := filepath.Join(s.Dir, "session.lock")
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		err := os.Mkdir(lock, 0700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("ChatGPT credentials are locked; if no mods process is running, remove %s", lock)
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer os.Remove(lock)
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := s.Load()
	if err != nil {
		return err
	}
	if state.HostID == "" {
		state.HostID = "urn:uuid:" + uuid.NewString()
	}
	if err = fn(&state); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".accounts-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.Dir, "accounts.json"))
}
func (s Store) Select(ctx context.Context, id string) error {
	return s.update(ctx, func(st *State) error {
		for _, a := range st.Accounts {
			if a.ClientID == id {
				st.Active = id
				return nil
			}
		}
		return errors.New("unknown ChatGPT account registration")
	})
}
