package reader

import "fmt"

type Error struct {
	Line    int
	Col     int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Col, e.Message)
}
