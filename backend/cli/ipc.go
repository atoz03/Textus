package cli

import (
	"bufio"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The connection between `textus` and the running app: a Unix socket on
// macOS and Linux, a named pipe on Windows, both only for this user. The
// command sends one JSON line; the app answers with lines of output and
// ends with the exit code.

// Request is one command for the app.
type Request struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// Cwd is the terminal's folder, for relative paths.
	Cwd  string `json:"cwd"`
	JSON bool   `json:"json"`
}

// Reply is one line of the app's answer: {"out": text}, {"err": text}, or
// last {"exit": code}.
type Reply struct {
	// Kind is "out", "err" or "exit".
	Kind string
	Text string
	Code int
}

func (r Reply) MarshalJSON() ([]byte, error) {
	if r.Kind == "exit" {
		return json.Marshal(map[string]int{"exit": r.Code})
	}
	return json.Marshal(map[string]string{r.Kind: strings.ToValidUTF8(r.Text, "�")})
}

func (r *Reply) UnmarshalJSON(raw []byte) error {
	var one map[string]jsontext.Value
	if err := json.Unmarshal(raw, &one); err != nil {
		return err
	}
	if len(one) != 1 {
		return errors.New("a reply has one field")
	}
	for kind, value := range one {
		r.Kind = kind
		switch kind {
		case "out", "err":
			return json.Unmarshal(value, &r.Text)
		case "exit":
			return json.Unmarshal(value, &r.Code)
		}
	}
	return errors.New("not a reply")
}

// Answer is what a command in the app came to.
type Answer struct {
	Out, Err string
	Exit     int
}

// OK is an answer that succeeded with out.
func OK(out string) Answer { return Answer{Out: out} }

// Fail is an answer that failed with err, exiting with exit.
func Fail(err string, exit int) Answer { return Answer{Err: err, Exit: exit} }

// Replies are the answer's lines, the exit last.
func (a Answer) Replies() []Reply {
	var replies []Reply
	if a.Out != "" {
		replies = append(replies, Reply{Kind: "out", Text: a.Out})
	}
	if a.Err != "" {
		replies = append(replies, Reply{Kind: "err", Text: a.Err})
	}
	return append(replies, Reply{Kind: "exit", Code: a.Exit})
}

// Exchange sends request and hands each reply line to onReply: the exit
// code.
func Exchange(conn io.ReadWriter, request Request, onReply func(Reply)) (int, error) {
	line, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return 0, fmt.Errorf("Textus couldn't be reached: %w", err)
	}
	reader := bufio.NewReader(conn)
	for {
		text, err := reader.ReadString('\n')
		if err != nil && (err != io.EOF || text == "") {
			if err == io.EOF {
				return 0, errors.New("Textus closed the connection without an answer.")
			}
			return 0, fmt.Errorf("Textus stopped answering: %w", err)
		}
		var reply Reply
		if json.Unmarshal([]byte(strings.TrimRight(text, "\r\n")), &reply) != nil {
			return 0, errors.New("Textus answered with something unreadable.")
		}
		if reply.Kind == "exit" {
			return reply.Code, nil
		}
		onReply(reply)
	}
}

// maxRequest bounds the one request line, so a stray client can't fill
// memory.
const maxRequest = 1 << 20

// Serve is the app's side of one connection: it reads the request, answers
// it with handle and writes the replies.
func Serve(conn io.ReadWriter, handle func(Request) Answer) error {
	line, err := bufio.NewReader(io.LimitReader(conn, maxRequest)).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	var request Request
	answer := Fail("Not a request this Textus understands.", 2)
	if json.Unmarshal([]byte(strings.TrimRight(line, "\r\n")), &request) == nil {
		answer = handle(request)
	}
	var out []byte
	for _, reply := range answer.Replies() {
		text, err := json.Marshal(reply)
		if err != nil {
			return err
		}
		out = append(append(out, text...), '\n')
	}
	_, err = conn.Write(out)
	return err
}
