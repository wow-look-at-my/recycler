package tml

import (
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// inspection is the process's own inspector. Every View that Load builds is adopted by it, so recording is not a mode
var inspection = &session{}

type session struct {
	mu      sync.Mutex
	insp    *Inspector
	serving bool
	driven  bool
	err     error
}

// isDriven reports whether something handed this process's program over.
func (s *session) isDriven() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.driven
}

// adopt takes over a freshly loaded View, and opens the socket the leading time there is a frame to answer about. The
func (s *session) adopt(v *View) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insp == nil {
		s.insp = newInspector(v)
	} else {
		s.insp.Attach(v)
	}
	if s.serving {
		return nil
	}
	s.serving = true
	socket := SocketPath()
	err := prepareSocketDir(socket)
	if err == nil {
		err = s.insp.ListenSocket(socket)
	}
	if err == nil {
		return nil
	}
	s.err = fmt.Errorf("tml: serve the inspection protocol on %s: %w", socket, err)
	return s.err
}

// drive gives the protocol the several capabilities that need a running program.
func (s *session) drive(p *tea.Program) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.driven = true
	if s.insp == nil {
		return
	}
	s.insp.OnKey(func(key string) error {
		p.Send(KeyMsg(key))
		return nil
	})
	s.insp.OnClick(func(x, y int) error {
		p.Send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		return nil
	})
	s.insp.OnRepaint(func() error {
		p.Send(RepaintMsg{})
		return nil
	})
}

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insp != nil {
		_ = s.insp.Close()
	}
	s.serving = false
}

// InspectError reports why the inspection socket is not being served, or nil. A program that asked for a socket by
func InspectError() error {
	inspection.mu.Lock()
	defer inspection.mu.Unlock()
	return inspection.err
}

// RepaintMsg asks the program to draw again. The inspector sends it after an override, and it is the only message a
type RepaintMsg struct{}

// Run runs a Bubble Tea program with the inspection protocol wired. It exists so that wiring is not a thing to
func NewProgram(m tea.Model, opts ...tea.ProgramOption) (*tea.Program, error) {
	p := tea.NewProgram(m, opts...)
	inspection.drive(p)
	return p, InspectError()
}

// Run is NewProgram plus running it, for a host with nothing to say to the program in between. A host that needs the
func Run(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) {
	p, err := NewProgram(m, opts...)
	if err != nil {
		return nil, err
	}
	defer inspection.close()
	return p.Run()
}
