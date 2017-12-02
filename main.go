package main

import (
	"context"

	"github.com/jacklaaa89/skybet/data"
	"github.com/davecgh/go-spew/spew"
)

type Object struct {
	Key string
	Secret string
}

func main() {
	config := &data.Config{Dir: "/Users/jacktimblin/test"}
	store, err := data.New(context.Background(), config)
	if err != nil {
		panic(err)
	}

	var ob Object
	err = store.Get("object", &ob)
	spew.Dump(err, ob)
}
