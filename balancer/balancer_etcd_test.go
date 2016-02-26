package balancer

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/weaveworks/flux/balancer/eventlogger"
	"github.com/weaveworks/flux/balancer/model"
	"github.com/weaveworks/flux/common/daemon"
	"github.com/weaveworks/flux/common/data"
	"github.com/weaveworks/flux/common/etcdutil"
	"github.com/weaveworks/flux/common/store/etcdstore"
	"github.com/weaveworks/flux/common/test/embeddedetcd"
)

func TestEtcdRestart(t *testing.T) {
	server, err := embeddedetcd.NewSimpleEtcd()
	require.Nil(t, err)
	defer func() { require.Nil(t, server.Destroy()) }()

	c, err := etcdutil.NewClient(server.URL())
	require.Nil(t, err)
	st := etcdstore.New(c)

	mipt := newMockIPTables(t)
	done := make(chan model.ServiceUpdate, 10)
	d := BalancerDaemon{
		errorSink:    daemon.NewErrorSink(),
		ipTablesCmd:  mipt.cmd,
		eventHandler: eventlogger.EventLogger{},
		netConfig: netConfig{
			chain:  "FLUX",
			bridge: "lo",
		},
		done: done,
	}
	d.setStore(st, 100*time.Millisecond)

	// Start the balancer and wait for it to process the initial
	// empty update
	d.Start()
	require.True(t, (<-done).Reset)
	require.Empty(t, d.errorSink)

	// Add a service and instance, and check that the balancer
	// heard about it
	require.Nil(t, st.AddService("svc", data.Service{
		Address:  "127.42.0.1",
		Port:     8888,
		Protocol: "tcp",
	}))
	require.False(t, (<-done).Reset)

	// Stop and restart the etcd server
	require.Nil(t, server.Stop())
	time.Sleep(100 * time.Millisecond)
	require.Nil(t, server.Start())
	time.Sleep(200 * time.Millisecond)

	// The reconnection should lead to a reset update
	require.True(t, (<-done).Reset)

	require.Nil(t, st.AddInstance("svc", "inst", data.Instance{
		Address: "127.0.0.1",
		Port:    10000,
		State:   data.LIVE,
	}))
	require.False(t, (<-done).Reset)

	// Verify that we are forwarding on the service IP
	require.Len(t, mipt.chains["nat FLUX"], 1)
	require.Len(t, mipt.chains["filter FLUX"], 0)
	// NB regexp related to service IP and port given in test case
	require.Regexp(t, "^-p tcp -d 127\\.42\\.0\\.1 --dport 8888 -j DNAT --to-destination 127\\.0\\.0\\.1:\\d+$", strings.Join(mipt.chains["nat FLUX"][0], " "))

	d.Stop()
	require.Empty(t, d.errorSink)
}