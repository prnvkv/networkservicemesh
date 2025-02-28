package tests

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"golang.org/x/net/context"

	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/apis/connectioncontext"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/apis/local/connection"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/apis/local/networkservice"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/apis/registry"
	connection2 "github.com/networkservicemesh/networkservicemesh/controlplane/pkg/apis/remote/connection"
)

func TestHealLocalDataplane(t *testing.T) {
	RegisterTestingT(t)

	storage := newSharedStorage()
	srv := newNSMDFullServer(Master, storage, defaultClusterConfiguration)
	srv2 := newNSMDFullServer(Worker, storage, defaultClusterConfiguration)
	defer srv.Stop()
	defer srv2.Stop()

	srv.testModel.AddDataplane(testDataplane1)
	srv2.testModel.AddDataplane(testDataplane2)

	// Register in both
	nseReg := srv2.registerFakeEndpointWithName("golden_network", "test", Worker, "ep1")

	// Add to local endpoints for Server2
	srv2.testModel.AddEndpoint(nseReg)

	l1 := newTestConnectionModelListener()
	l2 := newTestConnectionModelListener()

	srv.testModel.AddListener(l1)
	srv2.testModel.AddListener(l2)

	// Now we could try to connect via Client API
	nsmClient, conn := srv.requestNSMConnection("nsm-1")
	defer conn.Close()

	request := &networkservice.NetworkServiceRequest{
		Connection: &connection.Connection{
			NetworkService: "golden_network",
			Context: &connectioncontext.ConnectionContext{
				IpContext: &connectioncontext.IPContext{
					DstIpRequired: true,
					SrcIpRequired: true,
				},
			},
			Labels: make(map[string]string),
		},
		MechanismPreferences: []*connection.Mechanism{
			{
				Type: connection.MechanismType_KERNEL_INTERFACE,
				Parameters: map[string]string{
					connection.NetNsInodeKey:    "10",
					connection.InterfaceNameKey: "icmp-responder1",
				},
			},
		},
	}

	nsmResponse, err := nsmClient.Request(context.Background(), request)
	Expect(err).To(BeNil())
	Expect(nsmResponse.GetNetworkService()).To(Equal("golden_network"))

	// We need to check for cross connections.
	clientConnection1 := srv.testModel.GetClientConnection(nsmResponse.GetId())
	Expect(clientConnection1.GetID()).To(Equal("1"))
	Expect(clientConnection1.Xcon.GetRemoteDestination().GetMechanism().GetParameters()[connection2.VXLANSrcIP]).To(Equal("127.0.0.1"))

	clientConnection2 := srv2.testModel.GetClientConnection(clientConnection1.Xcon.GetRemoteDestination().GetId())
	Expect(clientConnection2.GetID()).To(Equal("1"))

	timeout := time.Second * 10

	l1.WaitAdd(1, timeout, t)
	// We need to inform cross connection monitor about this connection, since dataplane is fake one.
	epName := clientConnection1.Endpoint.NetworkserviceEndpoint.GetEndpointName()
	_, err = srv.nseRegistry.RemoveNSE(context.Background(), &registry.RemoveNSERequest{
		EndpointName: epName,
	})
	if err != nil {
		t.Fatal("Err must be nil")
	}

	// Simlate dataplane dead
	srv.testModel.AddDataplane(testDataplane1_1)
	srv.testModel.DeleteDataplane(testDataplane1.RegisteredName)

	// We need to inform cross connection monitor about this connection, since dataplane is fake one.
	// First update is with down state
	// But we want to wait for Up state
	l1.WaitUpdate(8, timeout, t)
	// We need to inform cross connection monitor about this connection, since dataplane is fake one.

	clientConnection1_1 := srv.testModel.GetClientConnection(nsmResponse.GetId())
	Expect(clientConnection1_1 != nil).To(Equal(true))
	Expect(clientConnection1_1.GetID()).To(Equal("1"))
	Expect(clientConnection1_1.Xcon.GetRemoteDestination().GetId()).To(Equal("1"))
	Expect(clientConnection1_1.Xcon.GetRemoteDestination().GetNetworkServiceEndpointName()).To(Equal(epName))
	Expect(clientConnection1_1.Xcon.GetRemoteDestination().GetMechanism().GetParameters()[connection2.VXLANSrcIP]).To(Equal("127.0.0.7"))
}
