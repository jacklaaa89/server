package object

import (
	"net/http"

	"github.com/google/uuid"
)

// response a generic response struct.
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

// List the response for a list of users.
type List struct {
	Response
	Users []User `json:"users"`
}

// Get response from a get single user request.
type Get struct {
	Response
	User User `json:"user"`
}

// post response from inserting a new user
// the id of the generated user is returned.
type Post struct {
	Response
	Id uuid.UUID `json:"id"`
}

/* Helper functions to generate responses. */

// NewGet generates a generic get response with the supplied user.
func NewGet(u User) Get {
	return Get{Response: WithSuccess(""), User: u}
}

// NewPost generates a generic post response with the newly generated id.
func NewPost(id uuid.UUID) Post {
	return Post{Response: WithSuccess(""), Id: id}
}

// NewList generates a generic list response.
func NewList(code int) List {
	return List{Response: Response{Code: code}, Users: make([]User, 0)}
}

// WithError returns a response with the supplied error.
func WithError(err error) Response {
	return Response{Code: http.StatusBadRequest, Message: err.Error()}
}

// WithSuccess returns a successful response with a message.
func WithSuccess(msg string) Response {
	return Response{Code: http.StatusOK, Message: msg}
}
