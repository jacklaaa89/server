package object

// User a representation of a person, a person has a firstname
// and a surname.
type User struct {
	FirstName string `msgpack:"first" json:"first"`
	Surname   string `msgpack:"last" json:"last"`
}
