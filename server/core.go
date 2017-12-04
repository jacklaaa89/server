package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jacklaaa89/server/data"
	"github.com/jacklaaa89/server/object"
	"github.com/jacklaaa89/server/template"
)

const (
	// defaultPort the default port to listen to.
	defaultPort = 8080
	// apiEndpoint the endpoint for the user api.
	apiEndpoint = "/api/user"
	// indexTemplate the key to the index static template.
	indexTemplate = "index.html"
)

// errClosed error returned when the store is closed.
var errClosed = errors.New("closed")

// Config server config.
type Config struct {
	Port int          `json:"port"`
	Data *data.Config `json:"data"`
}

// NewDefaultConfig initialises config with default values.
func NewDefaultConfig() *Config {
	return &Config{
		Port: defaultPort,
		Data: data.NewDefaultConfig(),
	}
}

// New initialises a new server.
func New(ctx context.Context, c *Config) (Server, error) {
	// initialise our internal server instance.
	s := &server{ctx: ctx}

	// initialise the data store.
	store, err := data.New(ctx, c.Data)
	if err != nil {
		return nil, err
	}
	s.store = store

	// initialise gin.
	router := gin.Default()

	// define the index route.
	router.GET("/", s.index)

	// define api routes.
	group := router.Group(apiEndpoint)
	{
		group.GET("/", s.list)
		group.PATCH("/:id", s.patch)
		group.POST("/", s.post)
		group.GET("/:id", s.get)
		group.DELETE("/:id", s.remove)
	}

	// initialise a new http.Server
	s.svr = &http.Server{
		Handler: router,
		Addr:    ":" + strconv.Itoa(c.Port),
	}

	// get signalled on close.
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, os.Kill)

	// register a on shutdown listener.
	s.svr.RegisterOnShutdown(s.close)

	// handle shutting down the server gracefully.
	go func() error {
		for {
			select {
			case <-s.ctx.Done():
				return s.Close()
			case <-signals:
				return s.Close()
			}
		}
	}()

	return s, nil
}

// Server an interface to a server.
type Server interface {
	io.Closer
	// Listens on the specified port.
	Listen() error
}

// server our instance of a server.
//
// This server implementation uses the standard http package
// using the Gin framework as the Router. Using this structure
// we are able to get access to the graceful shutdown methods
// added to http.Server in go v1.8+
//
// Gin gives some good debugging and logging tools out of the box.
//
// See: https://github.com/gin-gonic/gin
type server struct {
	// mutex to control access.
	sync.RWMutex
	// closed whether the server is closed.
	closed bool
	// svr the internal http.Server
	svr *http.Server
	// ctx the internal context.
	// if the internal context is Done(), then the
	// server is closed gracefully.
	ctx context.Context
	// store the internal data store.
	store data.Store
}

// ViewData data to pass to the index view.
type ViewData struct {
	Endpoint string
}

// index serves static content.
//
// This func returns the static index page for the application.
// This could be replaced with a JS client frontend including
// vueJS or React running on node.
//
// This api would then be the back end to add/update user records in a
// micro-service architechture.
//
// For simplicity the static template is loaded as an asset into
// the binary of this application as a const using go-bindata.
//
// See: https://github.com/jteeuwen/go-bindata
func (*server) index(c *gin.Context) {
	r, err := template.RenderAsset(indexTemplate, ViewData{Endpoint: apiEndpoint})
	if err != nil {
		c.AbortWithError(http.StatusNotFound, err)
		return
	}

	c.Render(http.StatusOK, r)
}

// list lists all of the users.
func (s *server) list(c *gin.Context) {
	var (
		users    = object.NewList(http.StatusOK)
		receiver object.User
	)

	iterator, _ := s.store.Iterate()
	for iterator.Next(&receiver) {
		// get the id for this iteration.
		k, err := iterator.Key()
		if err != nil {
			continue
		}
		// attempt to parse it into a uuid.
		id, err := uuid.Parse(k)
		if err != nil {
			continue
		}

		// set the id on the receiver object.
		receiver.ID = id
		users.Users = append(users.Users, receiver)
	}

	c.JSON(http.StatusOK, users)
}

// get gets a single user.
func (s *server) get(c *gin.Context) {
	u, err := s.getUser(c.Param("id"))
	if err != nil {
		s.withError(c, err)
		return
	}

	c.JSON(http.StatusOK, object.NewGet(*u))
}

// patch updates a user.
func (s *server) patch(c *gin.Context) {
	id := c.Param("id")
	u, err := s.getUser(id)
	if err != nil {
		s.withError(c, err)
		return
	}

	// bind the user with the request.
	if err := c.Bind(&u); err != nil {
		s.withError(c, err)
		return
	}

	// then update in the data-store.
	if err := s.store.Set(id, &u); err != nil {
		s.withError(c, err)
		return
	}

	c.JSON(http.StatusOK, object.WithSuccess("OK"))
}

// remove removes a user from the data-store.
func (s *server) remove(c *gin.Context) {
	id := c.Param("id")

	// ensure that the user exists.
	_, err := s.getUser(id)
	if err != nil {
		s.withError(c, err)
		return
	}

	// then update in the data-store.
	if err := s.store.Remove(id); err != nil {
		s.withError(c, err)
		return
	}

	c.JSON(http.StatusOK, object.WithSuccess("OK"))
}

// post inserts a new user.
func (s *server) post(c *gin.Context) {
	var user object.User
	// bind the user with the request.
	if err := c.Bind(&user); err != nil {
		s.withError(c, err)
		return
	}

	// generate a new UUID, insert the data and then
	// return the newly generated id.
	id := uuid.New()
	if err := s.store.Set(id.String(), user); err != nil {
		s.withError(c, err)
		return
	}

	c.JSON(http.StatusOK, object.NewPost(id))
}

// getUser attempts to get a user from the data store by the passed id.
func (s *server) getUser(id string) (*object.User, error) {
	var user object.User
	// enforce that an id is a valid UUID.
	uuid, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}

	err = s.store.Get(uuid.String(), &user)
	return &user, err
}

// withError helper function which writes a error response
// to the gin context with a http.StatusBadRequest error code.
func (s *server) withError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, object.WithError(err))
}

// Listen implements Server interface.
// This method blocks until the server is closed.
func (s *server) Listen() error {
	if s.isClosed() {
		return errClosed
	}

	return s.svr.ListenAndServe()
}

// Close implements io.Closer interface.
func (s *server) Close() error {
	if s.isClosed() {
		return errClosed
	}

	s.Lock()
	defer s.Unlock()

	return s.svr.Shutdown(s.ctx)
}

// isClosed determines if the server is closed.
func (s *server) isClosed() bool {
	s.Lock()
	defer s.Unlock()

	return s.closed
}

// close registered shutdown func, sets the closed
// status on the server.
func (s *server) close() {
	s.closed = true
	// ensure that the data store is closed.
	s.store.Close()
}
