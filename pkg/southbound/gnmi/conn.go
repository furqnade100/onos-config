// Copyright 2021-present Open Networking Foundation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gnmi

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onosproject/onos-lib-go/pkg/uri"

	"google.golang.org/grpc/connectivity"

	"google.golang.org/grpc"

	"github.com/onosproject/onos-lib-go/pkg/certs"

	"github.com/onosproject/onos-lib-go/pkg/errors"

	topoapi "github.com/onosproject/onos-api/go/onos/topo"

	"crypto/tls"

	"github.com/onosproject/onos-config/pkg/store/topo"
	"github.com/onosproject/onos-lib-go/pkg/logging"
	baseClient "github.com/openconfig/gnmi/client"
)

var log = logging.GetLogger("southbound", "gnmi")

// ConnID connection ID
type ConnID string

// Conn gNMI connection interface
type Conn interface {
	Client
	ID() ConnID
	TargetID() topoapi.ID
	State() connectivity.State
	Connect()
	WatchState(ctx context.Context, ch chan<- connectivity.State) error
}

// conn gNMI Connection
type conn struct {
	*client
	clientConn      *grpc.ClientConn
	id              ConnID
	targetID        topoapi.ID
	stateWatchers   []chan<- connectivity.State
	stateWatchersMu sync.RWMutex
	stateEventCh    chan connectivity.State
}

func newConnID() ConnID {
	connID := ConnID(uri.NewURI(
		uri.WithScheme("uuid"),
		uri.WithOpaque(uuid.New().String())).String())
	return connID
}

// WithTargetID sets target ID for a new connection
func WithTargetID(targetID topoapi.ID) func(conn *conn) {
	return func(conn *conn) {
		conn.targetID = targetID
	}
}

// WithClientConn sets client connection for a new connection
func WithClientConn(clientConn *grpc.ClientConn) func(conn *conn) {
	return func(conn *conn) {
		conn.clientConn = clientConn
	}
}

// WithClient sets the gnmi client for a new connection
func WithClient(client *client) func(conn *conn) {
	return func(conn *conn) {
		conn.client = client
	}
}

func newConn(options ...func(conn *conn)) *conn {
	conn := &conn{
		id:           newConnID(),
		stateEventCh: make(chan connectivity.State),
	}
	for _, option := range options {
		option(conn)
	}
	go conn.processStateEvents()
	return conn
}

// type manager interface {
// 	SaveInstance()
// }

// type gnmi_pkg struct {
// 	mgr manager
// }

// var mgr_inst gnmi_pkg

// func New_Manager(m manager) {
// 	mgr_inst.mgr = m
// }

func newDestination(target *topoapi.Object) (*baseClient.Destination, error) {
	asset := &topoapi.Asset{}
	err := target.GetAspect(asset)
	if err != nil {
		return nil, errors.NewInvalid("target entity %s must have 'onos.topo.Asset' aspect to work with onos-config", target.ID)
	}

	configurable := &topoapi.Configurable{}
	err = target.GetAspect(configurable)
	if err != nil {
		return nil, errors.NewInvalid("target entity %s must have 'onos.topo.Configurable' aspect to work with onos-config", target.ID)
	}

	mastership := &topoapi.MastershipState{}
	err = target.GetAspect(mastership)
	if err != nil {
		return nil, errors.NewInvalid("topo entity %s must have 'onos.topo.MastershipState' aspect to work with onos-config", target.ID)
	}

	tlsOptions := &topoapi.TLSOptions{}
	err = target.GetAspect(tlsOptions)
	if err != nil {
		return nil, errors.NewInvalid("topo entity %s must have 'onos.topo.TLSOptions' aspect to work with onos-config", target.ID)
	}
	//Furqan implement, get adapter associated to the device
	filter := topoapi.RelationFilter{
		RelationKind: "sessions",
		TargetKind:   "",
		Scope:        topoapi.RelationFilterScope_TARGET_ONLY,
	}
	filter.TargetId = "netconf-device-1"
	var nil_aspects []string
	//new topo connection
	opts, err := certs.HandleCertPaths("/etc/onos/certs/tls.cacrt", "/etc/onos/certs/tls.key", "/etc/onos/certs/tls.crt", true)
	if err != nil {
		log.Info(err)
	}
	// Create new topo store
	topoStore, err := topo.NewStore("onos-topo:5150", opts...)
	if err != nil {
		log.Info(err)
	}
	adapters, err := topoStore.List_with_filter(&topoapi.Filters{RelationFilter: &filter, WithAspects: nil_aspects})
	if err != nil {
		log.Info("Unable to retreive adapters from topo. Error: ", err)
	} else {
		for _, object := range adapters {
			switch object.Type {
			case topoapi.Object_ENTITY:
				log.Info("Furqan: object is entity. Kind ID: ", object.GetEntity().KindID)
				cfg := &topoapi.Configurable{}
				err = object.GetAspect(cfg)
				if err != nil {
					log.Info("couldnt get aspect for target")
				} else {
					log.Info("target address: ", cfg.Address)
					configurable.Address = cfg.Address
				}

			case topoapi.Object_RELATION:
				log.Info("Furqan: object is relation")
				r := object.GetRelation()
				log.Info("Object ID: ", object.ID, " Kind ID: ", r.KindID, " src: ", r.SrcEntityID, "target: ", r.TgtEntityID)
			}
		}
	}

	//objects, err := listObjects(cmd, &topoapi.Filters{RelationFilter: &filter, WithAspects: aspects}, topoapi.SortOrder_UNORDERED)

	destination := &baseClient.Destination{
		Addrs:   []string{configurable.Address},
		Target:  string(target.ID),
		Timeout: time.Duration(configurable.Timeout),
	}

	if tlsOptions.Plain {
		log.Info("Plain (non TLS) connection to ", configurable.Address)
	} else {
		tlsConfig := &tls.Config{}
		if tlsOptions.Insecure {
			log.Info("Insecure TLS connection to ", configurable.Address)
			tlsConfig = &tls.Config{InsecureSkipVerify: true}
		} else {
			log.Info("Secure TLS connection to ", configurable.Address)
		}
		if tlsOptions.CaCert == "" {
			log.Info("Loading default CA onfca")
			defaultCertPool, err := certs.GetCertPoolDefault()
			if err != nil {
				return nil, err
			}
			tlsConfig.RootCAs = defaultCertPool
		} else {
			certPool, err := certs.GetCertPool(tlsOptions.CaCert)
			if err != nil {
				return nil, err
			}
			tlsConfig.RootCAs = certPool
		}
		if tlsOptions.Cert == "" && tlsOptions.Key == "" {
			log.Info("Loading default certificates")
			clientCerts, err := tls.X509KeyPair([]byte(certs.DefaultClientCrt), []byte(certs.DefaultClientKey))
			if err != nil {
				return nil, err
			}
			tlsConfig.Certificates = []tls.Certificate{clientCerts}
		} else if tlsOptions.Cert != "" && tlsOptions.Key != "" {
			// Load certs given for device
			tlsConfig.Certificates = []tls.Certificate{setCertificate(tlsOptions.Cert, tlsOptions.Key)}
		} else {
			log.Errorf("Can't load Ca=%s , Cert=%s , key=%s for %v, trying with insecure connection",
				tlsOptions.CaCert, tlsOptions.Cert, tlsOptions.Key, configurable.Address)
			tlsConfig = &tls.Config{InsecureSkipVerify: true}
		}
		destination.TLS = tlsConfig
	}

	err = destination.Validate()
	if err != nil {
		return nil, err
	}

	return destination, nil
}

// ID returns the gNMI connection ID
func (c *conn) ID() ConnID {
	return c.id
}

// TargetID returns target ID associated with this connection
func (c *conn) TargetID() topoapi.ID {
	return c.targetID
}

// Connect connects to the target
func (c *conn) Connect() {
	c.clientConn.Connect()
}

// State returns connection state
func (c *conn) State() connectivity.State {
	return c.clientConn.GetState()
}

func (c *conn) WatchState(ctx context.Context, ch chan<- connectivity.State) error {
	c.stateWatchersMu.Lock()
	c.stateWatchers = append(c.stateWatchers, ch)
	c.stateWatchersMu.Unlock()

	go func() {
		<-ctx.Done()
		c.stateWatchersMu.Lock()
		stateWatchers := make([]chan<- connectivity.State, 0, len(c.stateWatchers)-1)
		for _, stateWatcher := range stateWatchers {
			if stateWatcher != ch {
				stateWatchers = append(stateWatchers, stateWatcher)
			}
		}
		c.stateWatchers = stateWatchers
		c.stateWatchersMu.Unlock()
	}()
	return nil

}

func (c *conn) processStateEvents() {
	log.Infof("Starting processing of connection state events for connection: %s", c.id)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := c.clientConn.GetState()
	log.Infof("Initial connection state for connection %s is %s", c.id, state.String())
	c.processStateEvent(state)
	for c.clientConn.WaitForStateChange(ctx, state) {
		state = c.clientConn.GetState()
		log.Infof("Connection state is changed for connection: %s, current state: %s", c.id, state.String())
		c.processStateEvent(state)
	}

}

func (c *conn) processStateEvent(state connectivity.State) {
	log.Infof("Notifying connection state for connection: %s", c.id)
	c.stateWatchersMu.RLock()
	for _, connStateWatcher := range c.stateWatchers {
		connStateWatcher <- state
	}
	c.stateWatchersMu.RUnlock()
}

var _ Conn = &conn{}
