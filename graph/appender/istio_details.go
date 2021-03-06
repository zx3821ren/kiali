package appender

import (
	"strings"

	"github.com/kiali/kiali/graph/tree"
	"github.com/kiali/kiali/kubernetes"
)

type IstioAppender struct{}

func (a IstioAppender) AppendGraph(trees *[]tree.ServiceNode, namespaceName string) {
	istioClient, err := kubernetes.NewClient()
	checkError(err)

	namespaceInfo := fetchNamespaceInfo(namespaceName, istioClient)

	for _, tree := range *trees {
		addRouteBadges(&tree, namespaceName, namespaceInfo)
	}
}

func fetchNamespaceInfo(namespaceName string, istioClient *kubernetes.IstioClient) *kubernetes.IstioDetails {
	istioDetails, err := istioClient.GetIstioDetails(namespaceName, "")
	checkError(err)

	return istioDetails
}

func addRouteBadges(n *tree.ServiceNode, namespaceName string, istioDetails *kubernetes.IstioDetails) {
	applyCircuitBreakers(n, namespaceName, istioDetails)
	applyRouteRules(n, namespaceName, istioDetails)

	for _, child := range n.Children {
		addRouteBadges(child, namespaceName, istioDetails)
	}
}

func applyCircuitBreakers(n *tree.ServiceNode, namespaceName string, istioDetails *kubernetes.IstioDetails) {
	serviceName := strings.Split(n.Name, ".")[0]
	version := n.Version

	found := false
	for _, destinationPolicy := range istioDetails.DestinationPolicies {
		if kubernetes.CheckDestinationPolicyCircuitBreaker(destinationPolicy, namespaceName, serviceName, version) {
			n.Metadata["hasCircuitBreaker"] = "true"
			found = true
			break
		}
	}

	// If we have found a CircuitBreaker from destinationPolicies we don't continue searching
	if !found {
		for _, destinationRule := range istioDetails.DestinationRules {
			if kubernetes.CheckDestinationRuleCircuitBreaker(destinationRule, namespaceName, serviceName, version) {
				n.Metadata["hasCircuitBreaker"] = "true"
				break
			}
		}
	}
}

func applyRouteRules(n *tree.ServiceNode, namespaceName string, istioDetails *kubernetes.IstioDetails) {
	serviceName := strings.Split(n.Name, ".")[0]
	version := n.Version

	found := false
	for _, routeRule := range istioDetails.RouteRules {
		if kubernetes.CheckRouteRule(routeRule, namespaceName, serviceName, version) {
			n.Metadata["hasRouteRule"] = "true"
			found = true
			break
		}
	}

	// If we have found a RouteRule we don't continue searching
	if !found {
		subsets := kubernetes.GetDestinationRulesSubsets(istioDetails.DestinationRules, serviceName, version)
		for _, virtualService := range istioDetails.VirtualServices {
			if kubernetes.CheckVirtualService(virtualService, namespaceName, serviceName, subsets) {
				n.Metadata["hasRouteRule"] = "true"
				break
			}
		}
	}
}
