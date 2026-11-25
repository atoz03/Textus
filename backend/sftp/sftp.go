// Package sftp is the client half of SFTP version 3 (draft-ietf-secsh-
// filexfer-02, which every OpenSSH server speaks): enough to list, read,
// write, create, rename and remove a server's files over a stream such as
// ssh's standard input and output. Requests go out together and their
// answers are matched by id, so reading and writing a large file waits on
// the link's speed, not on one round trip per piece.
package sftp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Packet types.
const (
	fxpInit     = 1
	fxpVersion  = 2
	fxpOpen     = 3
	fxpClose    = 4
	fxpRead     = 5
	fxpWrite    = 6
	fxpOpendir  = 11
	fxpReaddir  = 12
	fxpRemove   = 13
	fxpMkdir    = 14
	fxpRmdir    = 15
	fxpRealpath = 16
	fxpStat     = 17
	fxpRename   = 18
	fxpStatus   = 101
	fxpHandle   = 102
	fxpData     = 103
	fxpName     = 104
	fxpAttrs    = 105
)

// Open flags.
const (
	flagRead   = 0x01
	flagWrite  = 0x02
	flagCreate = 0x08
	flagTrunc  = 0x10
	flagExcl   = 0x20
)

// Status codes.
const (
	statusOK         = 0
	statusEOF        = 1
	statusNoSuchFile = 2
)

const (
	// chunk is what one read or write carries: what every server takes.
	chunk = 32 * 1024
	// window is how many reads or writes are out at once.
	window = 16
	// maxPacket is the longest answer taken: a folder's listing comes in
	// pieces far smaller. Anything longer is a stream that isn't SFTP (a
	// login script that prints).
	maxPacket = 4 * 1024 * 1024
	// requestTime is how long one answer may take.
	requestTime = 60 * time.Second
)

var statusNames = []string{
	"Ok", "End of file", "No such file", "Permission denied", "Failure", "Bad message", "No connection",
	"Connection lost", "Operation unsupported",
}

// StatusError is what the server answered a request with.
type StatusError struct {
	Code    uint32
	Message string
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if int(e.Code) < len(statusNames) {
		return statusNames[e.Code]
	}
	return fmt.Sprintf("SFTP error %d", e.Code)
}

// NotFound is whether err is the server saying there is no such file.
func NotFound(err error) bool {
	var s *StatusError
	return errors.As(err, &s) && s.Code == statusNoSuchFile
}

// ErrClosed is a request on a link that has ended.
var ErrClosed = errors.New("The connection was lost.")

type packet struct {
	kind byte
	data []byte
}

// Client is one SFTP link.
type Client struct {
	w io.WriteCloser
	// wmu keeps packets whole, one after the other. It is apart from mu:
	// a write may wait on a server that waits for its answers to be read.
	wmu sync.Mutex

	mu      sync.Mutex
	next    uint32
	pending map[uint32]chan packet
	ended   bool

	done chan struct{}
}

// New starts SFTP on a stream: r is what the server says, w what it is
// told. Closing the client closes w.
func New(r io.Reader, w io.WriteCloser) (*Client, error) {
	c := &Client{w: w, pending: map[uint32]chan packet{}, done: make(chan struct{})}
	if err := c.send(fxpInit, u32(nil, 3)); err != nil {
		return nil, err
	}
	p, err := readPacket(r)
	if err != nil {
		return nil, err
	}
	if p.kind != fxpVersion {
		return nil, fmt.Errorf("the server answered %d, not SFTP's version", p.kind)
	}
	go c.dispatch(r)
	return c, nil
}

func readPacket(r io.Reader) (packet, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return packet{}, err
	}
	n := binary.BigEndian.Uint32(head[:4])
	if n < 1 || n > maxPacket {
		return packet{}, fmt.Errorf("the server sent something that isn't SFTP (%d bytes long)", n)
	}
	data := make([]byte, n-1)
	if _, err := io.ReadFull(r, data); err != nil {
		return packet{}, err
	}
	return packet{head[4], data}, nil
}

// dispatch hands each answer to the request it answers, until the stream
// ends.
func (c *Client) dispatch(r io.Reader) {
	for {
		p, err := readPacket(r)
		if err != nil {
			break
		}
		if len(p.data) < 4 {
			continue
		}
		id := binary.BigEndian.Uint32(p.data)
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- packet{p.kind, p.data[4:]}
		}
	}
	c.mu.Lock()
	c.ended = true
	c.mu.Unlock()
	close(c.done)
}

// send writes one packet, whole.
func (c *Client) send(kind byte, payload []byte) error {
	b := make([]byte, 0, 5+len(payload))
	b = u32(b, uint32(len(payload)+1))
	b = append(b, kind)
	b = append(b, payload...)
	_, err := c.w.Write(b)
	return err
}

// request sends a request: its answer comes on the channel.
func (c *Client) request(kind byte, body []byte) (<-chan packet, error) {
	ch := make(chan packet, 1)
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()
	c.wmu.Lock()
	err := c.send(kind, append(u32(nil, id), body...))
	c.wmu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ErrClosed
	}
	return ch, nil
}

// answer waits for a request's answer.
func (c *Client) answer(ch <-chan packet) (packet, error) {
	timer := time.NewTimer(requestTime)
	defer timer.Stop()
	select {
	case p := <-ch:
		return p, nil
	case <-c.done:
		// An answer may have come just before the end.
		select {
		case p := <-ch:
			return p, nil
		default:
			return packet{}, ErrClosed
		}
	case <-timer.C:
		return packet{}, errors.New("The server took too long to answer.")
	}
}

// call sends a request and waits for its answer.
func (c *Client) call(kind byte, body []byte) (packet, error) {
	ch, err := c.request(kind, body)
	if err != nil {
		return packet{}, err
	}
	return c.answer(ch)
}

// status is a status answer as an error: nil for OK.
func status(p packet) error {
	if p.kind != fxpStatus {
		return fmt.Errorf("the server answered %d where a status was due", p.kind)
	}
	d := decoder{b: p.data}
	code := d.u32()
	message := d.str()
	if d.bad {
		return errors.New("the server's answer was cut short")
	}
	if code == statusOK {
		return nil
	}
	return &StatusError{code, message}
}

// Close ends the link.
func (c *Client) Close() error { return c.w.Close() }

// Done is closed when the link has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

func u32(b []byte, v uint32) []byte { return binary.BigEndian.AppendUint32(b, v) }

func u64(b []byte, v uint64) []byte { return binary.BigEndian.AppendUint64(b, v) }

func str(b []byte, s string) []byte { return append(u32(b, uint32(len(s))), s...) }

// decoder reads an answer's fields; bad once one is missing.
type decoder struct {
	b   []byte
	bad bool
}

func (d *decoder) u32() uint32 {
	if len(d.b) < 4 {
		d.bad = true
		return 0
	}
	v := binary.BigEndian.Uint32(d.b)
	d.b = d.b[4:]
	return v
}

func (d *decoder) u64() uint64 {
	if len(d.b) < 8 {
		d.bad = true
		return 0
	}
	v := binary.BigEndian.Uint64(d.b)
	d.b = d.b[8:]
	return v
}

func (d *decoder) str() string {
	n := d.u32()
	if d.bad || uint64(len(d.b)) < uint64(n) {
		d.bad = true
		return ""
	}
	s := string(d.b[:n])
	d.b = d.b[n:]
	return s
}
