// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package http

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/holisticode/swarm/api"
	"github.com/holisticode/swarm/chunk"
	"github.com/holisticode/swarm/state"
	"github.com/holisticode/swarm/storage"
	"github.com/holisticode/swarm/storage/feed"
	"github.com/holisticode/swarm/storage/localstore"
	"github.com/holisticode/swarm/storage/pin"
)

type TestServer interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

func NewTestSwarmServer(t *testing.T, serverFunc func(*api.API, *pin.API) TestServer, resolver api.Resolver,
	o *localstore.Options) *TestSwarmServer {

	swarmDir, err := ioutil.TempDir("", "swarm-storage-test")
	if err != nil {
		t.Fatal(err)
	}

	stateStore, err := state.NewDBStore(filepath.Join(swarmDir, "state-store.db"))
	if err != nil {
		t.Fatalf("could not create state store. Error: %s", err.Error())
	}

	localStore, err := localstore.New(swarmDir, make([]byte, 32), o)
	if err != nil {
		os.RemoveAll(swarmDir)
		t.Fatal(err)
	}

	tags := chunk.NewTags()
	fileStore := storage.NewFileStore(localStore, localStore, storage.NewFileStoreParams(), tags)

	// Swarm feeds test setup
	feedsDir, err := ioutil.TempDir("", "swarm-feeds-test")
	if err != nil {
		t.Fatal(err)
	}

	feeds, err := feed.NewTestHandlerWithStore(feedsDir, localStore, &feed.HandlerParams{})
	if err != nil {
		t.Fatal(err)
	}

	swarmApi := api.NewAPI(fileStore, resolver, nil, feeds.Handler, nil, tags)
	pinAPI := pin.NewAPI(localStore, stateStore, nil, tags, swarmApi)
	apiServer := httptest.NewServer(serverFunc(swarmApi, pinAPI))

	tss := &TestSwarmServer{
		Server:    apiServer,
		FileStore: fileStore,
		Tags:      tags,
		dir:       swarmDir,
		Hasher:    storage.MakeHashFunc(storage.DefaultHash)(),
		cleanup: func() {
			apiServer.Close()
			feeds.Close()
			os.RemoveAll(swarmDir)
			os.RemoveAll(feedsDir)
		},
		CurrentTime: 42,
	}
	feed.TimestampProvider = tss
	return tss
}

type TestSwarmServer struct {
	*httptest.Server
	Hasher      storage.SwarmHash
	FileStore   *storage.FileStore
	Tags        *chunk.Tags
	dir         string
	cleanup     func()
	CurrentTime uint64
}

func (t *TestSwarmServer) Close() {
	t.cleanup()
}

func (t *TestSwarmServer) Now() feed.Timestamp {
	return feed.Timestamp{Time: t.CurrentTime}
}
