package object

// Person a representation of a person, a person has a firstname
// and a surname.
type Person struct {
	FirstName string `json:"first_name"`
	Surname   string `json:"last_name"`
}
