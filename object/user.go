package object

import (
	"github.com/google/uuid"
)

// User a representation of a person, a person has a firstname
// and a surname.
type User struct {
	ID        uuid.UUID `msgpack:"-"     json:"id,omitempty"`
	FirstName string    `msgpack:"first" json:"first"`
	Surname   string    `msgpack:"last"  json:"last"`
}
