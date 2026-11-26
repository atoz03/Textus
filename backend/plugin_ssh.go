package backend

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"textus/backend/plugins"
	"textus/backend/plugins/ssh"
)

// textus.ssh's commands (backend/plugins/ssh): the hosts the user's ssh
// config names, a link to one for a plugin that is on with ssh, and the
// link's files over SFTP.

// maxSSHRead is the most a read hands over at once.
const maxSSHRead = 64 * 1024 * 1024

// sshLinks are the plugins' open links, by session.
type sshLinks struct {
	mu    sync.Mutex
	next  uint32
	links map[uint32]*ssh.Link
}

// sshGranted is nil when plugin id is on and holds ssh.
func (a *App) sshGranted(id string) error {
	if err := plugins.RequireID(id); err != nil {
		return err
	}
	if !a.pluginState().Allows(id, "ssh") {
		return plugins.Refused(id, "ssh")
	}
	return nil
}

// connectSSH opens a link for plugin id: its session. config replaces
// ~/.ssh/config (tests only).
func (a *App) connectSSH(id, host, config string) (uint32, error) {
	link, err := ssh.Open(id, host, config)
	if err != nil {
		return 0, err
	}
	s := &a.plug.ssh
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.links == nil {
		s.links = map[uint32]*ssh.Link{}
	}
	s.next++
	s.links[s.next] = link
	return s.next, nil
}

// sshLink is plugin id's own link session.
func (a *App) sshLink(id string, session uint32) (*ssh.Link, error) {
	s := &a.plug.ssh
	s.mu.Lock()
	defer s.mu.Unlock()
	if l := s.links[session]; l != nil && l.Plugin == id {
		return l, nil
	}
	return nil, errors.New("The connection was closed.")
}

// closeSSH closes the links of plugin id that keep says to, every link
// with no id.
func (a *App) closeSSH(keep func(id string, session uint32) bool) {
	s := &a.plug.ssh
	s.mu.Lock()
	var gone []*ssh.Link
	for session, l := range s.links {
		if !keep(l.Plugin, session) {
			gone = append(gone, l)
			delete(s.links, session)
		}
	}
	s.mu.Unlock()
	for _, l := range gone {
		l.Close()
	}
}

// sshArgs name a plugin's link and a path on its server.
type sshArgs struct {
	ID      string `json:"id"`
	Session uint32 `json:"session"`
	Path    string `json:"path"`
}

// sshPath is the link args name and their path on its server.
func (a *App) sshPath(args sshArgs) (*ssh.Link, string, error) {
	link, err := a.sshLink(args.ID, args.Session)
	if err != nil {
		return nil, "", err
	}
	path, err := ssh.Remote(args.Path)
	if err != nil {
		return nil, "", err
	}
	return link, path, nil
}

// RemoteEntry is one entry of a folder on the server (RemoteEntry in
// src/platform/bindings/plugin-access.ts).
type RemoteEntry struct {
	Name   string `json:"name"`
	Folder bool   `json:"folder"`
}

func init() {
	// The hosts named in the user's ~/.ssh/config.
	command("plugin_ssh_hosts", func(c *Call, a struct {
		ID string `json:"id"`
	}) ([]string, error) {
		if err := c.app.sshGranted(a.ID); err != nil {
			return nil, err
		}
		home := os.Getenv("HOME")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		if home == "" {
			return []string{}, nil
		}
		dir := filepath.Join(home, ".ssh")
		return ssh.HostsIn(filepath.Join(dir, "config"), dir), nil
	})
	// A link to host, through the user's own ssh and its config.
	command("plugin_ssh_connect", func(c *Call, a struct {
		ID   string `json:"id"`
		Host string `json:"host"`
	}) (uint32, error) {
		if err := c.app.sshGranted(a.ID); err != nil {
			return 0, err
		}
		return c.app.connectSSH(a.ID, a.Host, "")
	})
	// The folder the link starts in: the user's home there.
	command("plugin_ssh_home", func(c *Call, a sshArgs) (string, error) {
		link, err := c.app.sshLink(a.ID, a.Session)
		if err != nil {
			return "", err
		}
		return link.SFTP.RealPath(".")
	})
	command("plugin_ssh_list", func(c *Call, a sshArgs) ([]RemoteEntry, error) {
		link, path, err := c.app.sshPath(a)
		if err != nil {
			return nil, err
		}
		entries, err := link.SFTP.ReadDir(path)
		if err != nil {
			return nil, err
		}
		out := []RemoteEntry{}
		for _, e := range entries {
			if e.Name != "." && e.Name != ".." && !strings.Contains(e.Name, "/") {
				out = append(out, RemoteEntry{Name: e.Name, Folder: e.Attrs.IsDir()})
			}
		}
		return out, nil
	})
	// A file's bytes (callBytes in src/platform/ipc-call.ts).
	command("plugin_ssh_read", func(c *Call, a sshArgs) ([]byte, error) {
		link, path, err := c.app.sshPath(a)
		if err != nil {
			return nil, err
		}
		attrs, err := link.SFTP.Stat(path)
		if err != nil {
			return nil, err
		}
		if attrs.Size != nil && *attrs.Size > maxSSHRead {
			return nil, fmt.Errorf("%s is too big to open.", path)
		}
		return link.SFTP.ReadFile(path, maxSSHRead)
	})
	// Writes a file whole; base64 for bytes.
	command("plugin_ssh_write", func(c *Call, a struct {
		sshArgs
		Contents string `json:"contents"`
		Base64   bool   `json:"base64"`
	}) (nothing, error) {
		link, err := c.app.sshLink(a.ID, a.Session)
		if err != nil {
			return nil, err
		}
		data := []byte(a.Contents)
		if a.Base64 {
			if data, err = base64.StdEncoding.DecodeString(a.Contents); err != nil {
				return nil, err
			}
		}
		path, err := ssh.Remote(a.Path)
		if err != nil {
			return nil, err
		}
		return nil, link.SFTP.WriteFile(path, data)
	})
	// A new empty file or folder; a name taken is refused.
	command("plugin_ssh_create", func(c *Call, a struct {
		sshArgs
		Folder bool `json:"folder"`
	}) (nothing, error) {
		link, path, err := c.app.sshPath(a.sshArgs)
		if err != nil {
			return nil, err
		}
		if there, err := link.SFTP.Exists(path); err != nil || there {
			return nil, orElse(err, fmt.Errorf("%s already exists.", path))
		}
		if a.Folder {
			return nil, link.SFTP.Mkdir(path)
		}
		return nil, link.SFTP.Create(path)
	})
	// Moves or renames; a name taken is refused.
	command("plugin_ssh_rename", func(c *Call, a struct {
		ID      string `json:"id"`
		Session uint32 `json:"session"`
		From    string `json:"from"`
		To      string `json:"to"`
	}) (nothing, error) {
		link, err := c.app.sshLink(a.ID, a.Session)
		if err != nil {
			return nil, err
		}
		to, err := ssh.Remote(a.To)
		if err != nil {
			return nil, err
		}
		if there, err := link.SFTP.Exists(to); err != nil || there {
			return nil, orElse(err, fmt.Errorf("%s already exists.", to))
		}
		from, err := ssh.Remote(a.From)
		if err != nil {
			return nil, err
		}
		return nil, link.SFTP.Rename(from, to)
	})
	// Deletes a file, or an empty folder, for good.
	command("plugin_ssh_remove", func(c *Call, a sshArgs) (nothing, error) {
		link, path, err := c.app.sshPath(a)
		if err != nil {
			return nil, err
		}
		attrs, err := link.SFTP.Stat(path)
		if err != nil {
			return nil, err
		}
		if attrs.IsDir() {
			return nil, link.SFTP.Rmdir(path)
		}
		return nil, link.SFTP.Remove(path)
	})
	// Closes one link, or (no session) every link of the plugin.
	command("plugin_ssh_close", func(c *Call, a struct {
		ID      string  `json:"id"`
		Session *uint32 `json:"session"`
	}) (nothing, error) {
		c.app.closeSSH(func(id string, session uint32) bool {
			return id != a.ID || a.Session != nil && *a.Session != session
		})
		return nil, nil
	})
}

// orElse is err when there is one, else instead.
func orElse(err, instead error) error {
	if err != nil {
		return err
	}
	return instead
}
