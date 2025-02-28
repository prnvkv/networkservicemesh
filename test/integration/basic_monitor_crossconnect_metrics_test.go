// +build basic

package nsmd_integration_tests

import (
	"context"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/apis/crossconnect"

	"testing"
	"time"

	"github.com/networkservicemesh/networkservicemesh/dataplane/pkg/common"
	"github.com/networkservicemesh/networkservicemesh/test/kubetest"
	"github.com/networkservicemesh/networkservicemesh/test/kubetest/pods"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

func TestSimpleMetrics(t *testing.T) {
	RegisterTestingT(t)
	if testing.Short() {
		t.Skip("Skip, please run without -short")
		return
	}
	k8s, err := kubetest.NewK8s(true)
	Expect(err).To(BeNil())

	defer k8s.Cleanup()

	nodesCount := 2
	requestPeriod := time.Second

	nodes, err := kubetest.SetupNodesConfig(k8s, nodesCount, defaultTimeout, []*pods.NSMgrPodConfig{
		{
			DataplaneVariables: map[string]string{
				common.DataplaneMetricsEnabledKey:       "true",
				common.DataplaneMetricsRequestPeriodKey: requestPeriod.String(),
			},
			Variables: pods.DefaultNSMD(),
		},
	}, k8s.GetK8sNamespace())
	k8s.WaitLogsContains(nodes[0].Dataplane, nodes[0].Dataplane.Spec.Containers[0].Name, "Metrics collector: creating notificaiton client", time.Minute)
	Expect(err).To(BeNil())
	kubetest.DeployICMP(k8s, nodes[nodesCount-1].Node, "icmp-responder-nse-1", defaultTimeout)
	defer kubetest.ShowLogs(k8s, t)

	eventCh, closeFunc := kubetest.XconProxyMonitor(k8s, nodes[0], "0")
	defer closeFunc()

	metricsCh := metricsFromEventCh(eventCh)
	nsc := kubetest.DeployNSC(k8s, nodes[0].Node, "nsc1", defaultTimeout)

	response, _, err := k8s.Exec(nsc, nsc.Spec.Containers[0].Name, "ping", "172.16.1.2", "-A", "-c", "4")
	logrus.Infof("response = %v", response)
	Expect(err).To(BeNil())
	<-time.After(requestPeriod * 5)
	k8s.DeletePods(nsc)
	select {
	case metrics := <-metricsCh:
		Expect(isMetricsEmpty(metrics)).Should(Equal(false))
		Expect(metrics["rx_error_packets"]).Should(Equal("0"))
		Expect(metrics["tx_error_packets"]).Should(Equal("0"))
		return
	case <-time.After(defaultTimeout):
		t.Fatalf("Fail to get metrics during %v", defaultTimeout)
	}
}

func crossConnectClient(address string) (crossconnect.MonitorCrossConnect_MonitorCrossConnectsClient, func()) {
	var err error
	logrus.Infof("Starting CrossConnections Monitor on %s", address)
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		Expect(err).To(BeNil())
		return nil, nil
	}
	monitorClient := crossconnect.NewMonitorCrossConnectClient(conn)
	stream, err := monitorClient.MonitorCrossConnects(context.Background(), &empty.Empty{})
	if err != nil {
		Expect(err).To(BeNil())
		return nil, nil
	}

	closeFunc := func() {
		if err := conn.Close(); err != nil {
			logrus.Errorf("Closing the stream with: %v", err)
		}
	}
	return stream, closeFunc
}

func metricsFromEventCh(eventCh <-chan *crossconnect.CrossConnectEvent) chan map[string]string {
	metricsCh := make(chan map[string]string)
	go func() {
		defer close(metricsCh)
		for {
			event, ok := <-eventCh
			if !ok {
				return
			}
			logrus.Infof("Received event %v", event)
			if event.Metrics == nil {
				continue
			}
			for k, v := range event.Metrics {
				logrus.Infof("New statistics: %v %v", k, v)
				if isMetricsEmpty(v.Metrics) {
					logrus.Infof("Statistics: %v %v is empty", k, v)
					continue
				}
				metricsCh <- v.Metrics
			}
		}
	}()
	return metricsCh
}

func isMetricsEmpty(metrics map[string]string) bool {
	for _, v := range metrics {
		if v != "0" && v != "" {
			return false
		}
	}
	return true
}
