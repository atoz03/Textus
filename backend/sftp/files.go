package sftp

import (
	"errors"
	"fmt"
)

// Attr flags.
const (
	attrSize        = 0x1
	attrUIDGID      = 0x2
	attrPermissions = 0x4
	attrTimes       = 0x8
	attrExtended    = 0x80000000
)

// modeDir is S_IFDIR in a file's permissions; modeType masks the kind.
const (
	modeType = 0o170000
	modeDir  = 0o040000
)

// Attrs is what the server says of a file.
type Attrs struct {
	Size *uint64
	// Mode is the file's kind and permissions, when given.
	Mode *uint32
}

// IsDir is whether the attributes are a folder's.
func (a Attrs) IsDir() bool { return a.Mode != nil && *a.Mode&modeType == modeDir }

func (d *decoder) attrs() Attrs {
	var a Attrs
	flags := d.u32()
	if flags&attrSize != 0 {
		size := d.u64()
		a.Size = &size
	}
	if flags&attrUIDGID != 0 {
		d.u32()
		d.u32()
	}
	if flags&attrPermissions != 0 {
		mode := d.u32()
		a.Mode = &mode
	}
	if flags&attrTimes != 0 {
		d.u32()
		d.u32()
	}
	if flags&attrExtended != 0 {
		for n := d.u32(); n > 0 && !d.bad; n-- {
			d.str()
			d.str()
		}
	}
	return a
}

// Entry is one name of a folder.
type Entry struct {
	Name  string
	Attrs Attrs
}

// names is a NAME answer's entries.
func names(p packet) ([]Entry, error) {
	if p.kind != fxpName {
		return nil, status(p)
	}
	d := decoder{b: p.data}
	n := d.u32()
	var out []Entry
	for i := uint32(0); i < n && !d.bad; i++ {
		name := d.str()
		d.str() // the long name, as ls -l would put it
		out = append(out, Entry{name, d.attrs()})
	}
	if d.bad {
		return nil, errors.New("the server's answer was cut short")
	}
	return out, nil
}

// handle is a HANDLE answer's handle.
func handle(p packet) (string, error) {
	if p.kind != fxpHandle {
		return "", status(p)
	}
	d := decoder{b: p.data}
	h := d.str()
	if d.bad {
		return "", errors.New("the server's answer was cut short")
	}
	return h, nil
}

// RealPath is path as the server names it in full: "." is the folder a
// link starts in.
func (c *Client) RealPath(path string) (string, error) {
	p, err := c.call(fxpRealpath, str(nil, path))
	if err != nil {
		return "", err
	}
	found, err := names(p)
	if err != nil {
		return "", err
	}
	if len(found) != 1 {
		return "", fmt.Errorf("the server named %d paths for %s", len(found), path)
	}
	return found[0].Name, nil
}

// Stat is what the server says of the file at path, through links.
func (c *Client) Stat(path string) (Attrs, error) {
	p, err := c.call(fxpStat, str(nil, path))
	if err != nil {
		return Attrs{}, err
	}
	if p.kind != fxpAttrs {
		return Attrs{}, status(p)
	}
	d := decoder{b: p.data}
	a := d.attrs()
	if d.bad {
		return Attrs{}, errors.New("the server's answer was cut short")
	}
	return a, nil
}

// Exists is whether something is at path.
func (c *Client) Exists(path string) (bool, error) {
	_, err := c.Stat(path)
	if err == nil {
		return true, nil
	}
	if NotFound(err) {
		return false, nil
	}
	return false, err
}

// simple is a request answered with a status.
func (c *Client) simple(kind byte, body []byte) error {
	p, err := c.call(kind, body)
	if err != nil {
		return err
	}
	return status(p)
}

// Mkdir makes a folder.
func (c *Client) Mkdir(path string) error { return c.simple(fxpMkdir, u32(str(nil, path), 0)) }

// Rmdir removes an empty folder.
func (c *Client) Rmdir(path string) error { return c.simple(fxpRmdir, str(nil, path)) }

// Remove deletes a file.
func (c *Client) Remove(path string) error { return c.simple(fxpRemove, str(nil, path)) }

// Rename moves from to to.
func (c *Client) Rename(from, to string) error { return c.simple(fxpRename, str(str(nil, from), to)) }

func (c *Client) open(path string, flags uint32) (string, error) {
	p, err := c.call(fxpOpen, u32(u32(str(nil, path), flags), 0))
	if err != nil {
		return "", err
	}
	return handle(p)
}

func (c *Client) close(h string) error { return c.simple(fxpClose, str(nil, h)) }

// ReadDir lists the folder at path.
func (c *Client) ReadDir(path string) ([]Entry, error) {
	p, err := c.call(fxpOpendir, str(nil, path))
	if err != nil {
		return nil, err
	}
	h, err := handle(p)
	if err != nil {
		return nil, err
	}
	defer c.close(h)
	var all []Entry
	for {
		p, err := c.call(fxpReaddir, str(nil, h))
		if err != nil {
			return nil, err
		}
		if p.kind == fxpStatus {
			if err := status(p); err != nil {
				var s *StatusError
				if errors.As(err, &s) && s.Code == statusEOF {
					return all, nil
				}
				return nil, err
			}
			return all, nil
		}
		found, err := names(p)
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}
}

// Create makes an empty file at path; one there already is an error.
func (c *Client) Create(path string) error {
	h, err := c.open(path, flagWrite|flagCreate|flagExcl)
	if err != nil {
		return err
	}
	return c.close(h)
}

// read is one read out: where, how much, and its answer.
type read struct {
	offset uint64
	gen    int
	answer <-chan packet
}

// ReadFile is the whole file at path, refused past limit bytes.
func (c *Client) ReadFile(path string, limit int) ([]byte, error) {
	h, err := c.open(path, flagRead)
	if err != nil {
		return nil, err
	}
	defer c.close(h)
	var data []byte
	var out []read
	next, gen, ended := uint64(0), 0, false
	for {
		for !ended && len(out) < window {
			ch, err := c.request(fxpRead, u32(u64(str(nil, h), next), chunk))
			if err != nil {
				return nil, err
			}
			out = append(out, read{next, gen, ch})
			next += chunk
		}
		if len(out) == 0 {
			return data, nil
		}
		r := out[0]
		out = out[1:]
		p, err := c.answer(r.answer)
		if err != nil {
			return nil, err
		}
		// Asked for before a short read moved the next offset back.
		if r.gen != gen {
			continue
		}
		if p.kind == fxpStatus {
			if err := status(p); err != nil {
				var s *StatusError
				if !errors.As(err, &s) || s.Code != statusEOF {
					return nil, err
				}
			}
			ended, gen = true, gen+1
			continue
		}
		if p.kind != fxpData {
			return nil, fmt.Errorf("the server answered %d where data was due", p.kind)
		}
		d := decoder{b: p.data}
		piece := d.str()
		if d.bad {
			return nil, errors.New("the server's answer was cut short")
		}
		if len(data)+len(piece) > limit {
			return nil, fmt.Errorf("%s is too big to open.", path)
		}
		data = append(data, piece...)
		if len(piece) == 0 {
			ended, gen = true, gen+1
		} else if len(piece) < chunk {
			// Less than asked: what was asked for after it starts again
			// from here.
			next, gen = r.offset+uint64(len(piece)), gen+1
		}
	}
}

// WriteFile writes data as the whole file at path, made if it isn't there.
func (c *Client) WriteFile(path string, data []byte) error {
	h, err := c.open(path, flagWrite|flagCreate|flagTrunc)
	if err != nil {
		return err
	}
	var out []<-chan packet
	wait := func() error {
		p, err := c.answer(out[0])
		out = out[1:]
		if err != nil {
			return err
		}
		return status(p)
	}
	var failed error
	for offset := 0; offset < len(data) && failed == nil; offset += chunk {
		piece := data[offset:min(offset+chunk, len(data))]
		ch, err := c.request(fxpWrite, str(u64(str(nil, h), uint64(offset)), string(piece)))
		if err != nil {
			return err
		}
		out = append(out, ch)
		if len(out) >= window {
			failed = wait()
		}
	}
	for len(out) > 0 && failed == nil {
		failed = wait()
	}
	if err := c.close(h); failed == nil {
		failed = err
	}
	return failed
}
