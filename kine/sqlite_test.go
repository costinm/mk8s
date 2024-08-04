package kine

import (
	"context"
	"log"
	"testing"

	"github.com/k3s-io/kine/pkg/client"
)

func TestSqlite(t *testing.T) {
	ep, err := Kine()
	if err != nil {
		t.Fatal(err)
	}

	log.Println(ep.Endpoints)

	c, err := client.New(ep)
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Get(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	log.Println(v)
	err = c.Create(context.Background(), "key", []byte("value"))
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.List(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	log.Println(res)
}
