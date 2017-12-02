package data

import (
	"context"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"time"
	"path/filepath"
	"reflect"

	"github.com/cenkalti/backoff"
	lock "github.com/nightlyone/lockfile"
	"github.com/vmihailenco/msgpack"
)

const (
	// indexFile the location of the index file.
	indexFile = "index.dat"
	// lockPrefix the prefix for a os lock file.
	lockPrefix = ".lock"
	// vExt the value extension.
	vExt = ".msgpack"
)

// errNoDirectory error returned when the supplied directory is
// actually not a directory.
var errNoDirectory = errors.New("not a directory")

// errClosed error returned when the store is closed.
var errClosed = errors.New("closed")

// errNoExists error returned when a value does not exist in the datastore.
var errNoExists = errors.New("does not exist")

// errNotPointer error returned when receiver is not a pointer.
var errNotPointer = errors.New("receiver is not a pointer")

// filterRegex regex to file keys of unsupported chars in keys.
var filterRegex = regexp.MustCompile("[^a-zA-Z0-9]+")

// Store store represents an interface to a data-store
// where objects are held.
// For this trivial example the store will be similar to that
// of redis data store except all of the entries are stored to disk.
// values are stored in a msg-pack format, so you can use the
// msgpack.CustomMarshaler/CustomUnmarshaler interfaces.
type Store interface {
	io.Closer
	// Get get a model from the store and populates the passed receiver.
	Get(string, interface{}) error
	// Set sets a model into the store and returns the newly generated ID.
	// if a duplicate model is found the object is updated.
	Set(string, interface{}) error
	// Keys returns the list of keys in the system.
	// bare in mind keys are formatted into hashes.
	Keys() ([]string, error)
	// Remove removes an element from the store.
	Remove(string) error
}

// Config configuration for the data store.
type Config struct {
	// Dir the directory where data is stored.
	Dir string `json:"directory"`
}

// indexUpdater callback which gives access to the index in a locked state.
type indexUpdater func(in *internal)

// internal an internal index.
type internal map[string]string

// index an internal index of the data in the store.
// this is a map to the id to the filepath of the entry
type index struct {
	// mutex to stop concurrent map read / writes.
	sync.RWMutex
	// m the internal map store.
	m internal
}

// unmarshal unmarshal's raw json data into the index.
func (i *index) unmarshal(d []byte) error {
	i.Lock()
	defer i.Unlock()

	return msgpack.Unmarshal(d, &i.m)
}

// marshal marshals the index into byte form.
func (i *index) marshal() ([]byte, error) {
	i.Lock()
	defer i.Unlock()

	return msgpack.Marshal(i.m)
}

// update applies an updater to an index.
func (i *index) update(updater indexUpdater) {
	i.Lock()
	defer i.Unlock()
	updater(&i.m)
}

// keys returns all of the keys in the index.
func (i *index) keys() []string {
	i.Lock()
	defer i.Unlock()

	keys := make([]string, len(i.m))
	var index int
	for k := range i.m {
		keys[index] = k
		index++
	}

	return keys
}

// get gets a record from the index.
func (i *index) get(k string) (string, error) {
	i.Lock()
	defer i.Unlock()

	s, ok := i.m[k]
	if !ok {
		return "", errNoExists
	}

	return s, nil
}

// fileStore an instance of a file store
// which implements the Store interface.
type fileStore struct {
	// a mutex when performing concurrent operations.
	sync.RWMutex
	// Config the config for the file store.
	Config *Config
	// dir the directory where data is stored.
	dir *os.File
	// closed whether the store is closed.
	closed bool
	// closeCh a channel to notify when the store is closed.
	closeCh chan bool
	// index the data store index used for fast data lookup.
	index index
	// ctx the context to control go routines spawned by the store.
	ctx context.Context
	// cancel the func to cancel the context.
	cancel context.CancelFunc
}

// load performs a load of the index.
func (f *fileStore) load() error {
	if f.isClosed() {
		return errClosed
	}

	// lock at os level.
	fl := <-f.lock(indexFile)
	if fl.Err != nil {
		return fl.Err
	}

	defer fl.l.Unlock()

	// load the index file and read it into memory.
	data, err := ioutil.ReadFile(filepath.Join(f.dir.Name(), indexFile))
	if err != nil {
		return err
	}

	return f.index.unmarshal(data)
}

// updateIndex updates the internal index.
func (f *fileStore) updateIndex(updater indexUpdater) error {
	// lock the write to the index.
	f.index.update(updater)
	// lock the index file write.
	fl := <-f.lock(indexFile)
	if fl.Err != nil {
		return fl.Err
	}
	defer fl.l.Unlock()

	// marshal up the current state of the index.
	d, err := f.index.marshal()
	if err != nil {
		return err
	}

	// write the index file.
	return ioutil.WriteFile(filepath.Join(f.dir.Name(), indexFile), d, os.ModePerm)
}

// represents a file lock
// contains an err if we failed to repeatedly obtain the file lock.
type fileLock struct {
	// Err an error returned from locking a file.
	Err error
	// l the locked file.
	l lock.Lockfile
}

// lock the returned channel blocks until we have access to
// write to the file store.
func (f *fileStore) lock(file string) <-chan fileLock {
	lc := make(chan fileLock)
	go func() {
		// attempt to open the lock file.
		l, err := lock.New(filepath.Join(os.TempDir(), filepath.Base(file) + lockPrefix))
		if err != nil {
			lc <- fileLock{Err: err}
			return
		}
		// attempt to try the lock with an exponential back-off policy.
		eb := backoff.WithMaxTries(backoff.NewExponentialBackOff(), 10)
		lc <- fileLock{Err: backoff.Retry(func() error {
			select {
			case <-f.ctx.Done():
				// if the context is done, we dont want to continue, but
				// we want an error to be returned.
				return backoff.Permanent(f.ctx.Err())
			default:
				// otherwise continue to try the lock.
				return l.TryLock()
			}
		}, eb), l: l}
	}()

	return (<-chan fileLock)(lc)
}

// replacement a placeholder for an empty string.
var replacement string

// hash prepares and hashes a string key to in an uint64.
func (f *fileStore) hash(k string) string {
	return strings.ToUpper(filterRegex.ReplaceAllString(k, replacement))
}

// Get implements Store interface.
func (f *fileStore) Get(k string, r interface{}) error {
	// ensure the store is still open.
	if f.isClosed() {
		return errClosed
	}

	f.Lock()
	defer f.Unlock()

	// ensure we have a pointer to Unmarshal into.
	if reflect.TypeOf(r).Kind() != reflect.Ptr {
		return errNotPointer
	}

	// attempt to get the raw data from the file system.
	d, err := f.get(f.hash(k))
	if err != nil {
		return err
	}

	// decode the data into the given receiver.
	return msgpack.Unmarshal(d, r)
}

// get gets the raw data from the data-store directory if it exists.
// WARNING - this method is not thread-safe. it should be used by methods
// which lock.
func (f *fileStore) get(i string) ([]byte, error) {
	// check the index for the file location.
	s, err := f.index.get(i)
	if err != nil {
		// if no index was found, generate the key.
		// in this example this is quite a trivial task, but
		// in a more robust system, finding if a document exists
		// may not be as easy as it seems. Also the index could contain
		// more in depth information so we could perform quick queries.
		s = f.fileName(i)
	}

	// attempt to read the file from disk.
	return ioutil.ReadFile(s)
}

func (f *fileStore) Set(k string, v interface{}) error {
	// ensure the store is still open.
	if f.isClosed() {
		return errClosed
	}

	f.Lock()
	defer f.Unlock()

	// encode the object.
	d, err := msgpack.Marshal(v)
	if err != nil {
		return err
	}

	// set the record in the datastore.
	return f.set(f.hash(k), d)
}

// fileName generates a filename for a entry.
func (f *fileStore) fileName(i string) string {
	return filepath.Join(f.dir.Name(), i + vExt)
}

// set sets data into the datastore directory.
func (f *fileStore) set(i string, d []byte) error {
	// write the file to the datastore.
	// update the index file.
	// update our internal index.
	fi := f.fileName(i)
	fl := <-f.lock(fi)
	if fl.Err != nil {
		return fl.Err
	}

	// write the file.
	err := ioutil.WriteFile(fi, d, os.ModePerm)
	lErr := fl.l.Unlock()
	if err != nil {
		return err
	}

	// handy func to revert writing the data
	// incase an error occurs performing
	// any subsequent actions.
	revert := func() {
		os.Remove(fi)
		// any more actions...
	}

	// check we successfully unlocked the lock file.
	if lErr != nil {
		revert()
		return lErr
	}

	// attempt to update the internal index.
	if err = f.updateIndex(func(in *internal) {
		(*in)[i] = fi
	}); err != nil {
		revert()
	}

	return err
}

// Keys returns all of the keys in the store.
func (f *fileStore) Keys() ([]string, error) {
	// this is where the index shines. we can just return all of the keys in
	// the index.
	if f.isClosed() {
		return nil, errClosed
	}

	f.Lock()
	defer f.Unlock()

	return f.index.keys(), nil
}

// Remove implements Store interface.
func (f *fileStore) Remove(k string) error {
	if f.isClosed() {
		return errClosed
	}

	f.Lock()
	defer f.Unlock()

	return f.remove(f.hash(k))
}

// remove removes a key from the store and the index.
func (f *fileStore) remove(k string) error {
	defer f.updateIndex(func(in *internal) {
		delete(*in, k)
	})

	fl := <-f.lock(k)
	if fl.Err != nil {
		return fl.Err
	}
	defer fl.l.Unlock()

	return os.Remove(f.fileName(k))
}

// Close implements io.Closer interface.
func (f *fileStore) Close() error {
	if f.isClosed() {
		return errClosed
	}

	f.closeCh <- true

	return nil
}

// isClosed determines if the store is closed.
func (f *fileStore) isClosed() bool {
	f.Lock()
	defer f.Unlock()

	return f.closed
}

// close closes the filestore.
func (f *fileStore) close() error {
	f.cancel()
	f.closed = true

	return f.dir.Close()
}

// New initialises a new data store or returns
// an error if it couldn't be opened.
func New(ctx context.Context, c *Config) (Store, error) {
	// open the file in the config in write mode.
	dir, err := os.Open(c.Dir)
	if err != nil {
		return nil, err
	}

	// ensure that we have a directory.
	if s, err := dir.Stat(); err != nil || !s.IsDir() {
		if err == nil {
			err = errNoDirectory
		}

		return nil, err
	}

	storeCtx, cancel := context.WithCancel(ctx)
	// initialise a new file store.
	f := &fileStore{
		Config:  c,
		dir:     dir,
		closeCh: make(chan bool),
		index:   index{m: make(map[string]string)},
		ctx:     storeCtx,
		cancel:  cancel,
	}

	// receive notifications on interrupt / kill.
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, os.Kill)

	// initially load index.
	f.load()

	// constantly rebuild the index.
	// for large index's this should not be the most
	// performant way of keeping the index up to date.
	// but this is fine for this trivial example.
	go func() {
		ticker := time.NewTicker(time.Second)
		for {
			select {
			// keep the index up to date.
			case <-ticker.C:
				f.load()
			// if we receive the signal, close.
			case <-signals:
				f.Close()
			// if the root context informs us that we are dead.
			case <-ctx.Done():
				f.Close()
			// if we receive a signal on the close channel.
			// close the directory and stop the ticker.
			case <-f.closeCh:
				f.close()
				return
			}
		}
	}()

	return f, nil
}
