package kubernetes

import (
	"github.com/kiali/kiali/config"
	"k8s.io/api/apps/v1beta1"
	autoscalingV1 "k8s.io/api/autoscaling/v1"
	"k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type servicesResponse struct {
	services *v1.ServiceList
	err      error
}

type serviceResponse struct {
	service *v1.Service
	err     error
}

type endpointsResponse struct {
	endpoints *v1.Endpoints
	err       error
}

type deploymentsResponse struct {
	deployments *v1beta1.DeploymentList
	err         error
}

type autoscalersResponse struct {
	autoscalers *autoscalingV1.HorizontalPodAutoscalerList
	err         error
}

type podsResponse struct {
	pods *v1.PodList
	err  error
}

type podResponse struct {
	pod *v1.Pod
	err error
}

// GetNamespaces returns a list of all namespaces of the cluster.
// It returns a list of all namespaces of the cluster.
// It returns an error on any problem.
func (in *IstioClient) GetNamespaces() (*v1.NamespaceList, error) {
	namespaces, err := in.k8s.CoreV1().Namespaces().List(emptyListOptions)
	if err != nil {
		return nil, err
	}

	return namespaces, nil
}

// GetServices returns a list of services for a given namespace.
// It returns an error on any problem.
func (in *IstioClient) GetServices(namespace string) (*ServiceList, error) {
	var err error
	servicesChan, podsChan, deploymentsChan := make(chan servicesResponse), make(chan podsResponse), make(chan deploymentsResponse)

	go in.getServiceList(namespace, servicesChan)
	go in.getPodsList(namespace, podsChan)
	go in.getDeployments(namespace, deploymentsChan)

	servicesResponse := <-servicesChan
	podsResponse := <-podsChan
	deploymentsResponse := <-deploymentsChan

	services := &ServiceList{}
	services.Services = servicesResponse.services
	services.Pods = podsResponse.pods
	services.Deployments = deploymentsResponse.deployments

	if servicesResponse.err != nil {
		err = servicesResponse.err
	} else if podsResponse.err != nil {
		err = podsResponse.err
	} else {
		err = deploymentsResponse.err
	}

	return services, err
}

// GetDeployments returns a list of deployments for a given namespace.
// It returns an error on any problem.
func (in *IstioClient) GetDeployments(namespace string) (*v1beta1.DeploymentList, error) {
	return in.k8s.AppsV1beta1().Deployments(namespace).List(emptyListOptions)
}

// GetService returns the definition of a specific service.
// It returns an error on any problem.
func (in *IstioClient) GetService(namespace, serviceName string) (*v1.Service, error) {
	return in.k8s.CoreV1().Services(namespace).Get(serviceName, emptyGetOptions)
}

// GetPods returns the pods definitions for a given set of labels.
// It returns an error on any problem.
func (in *IstioClient) GetPods(namespace string, labelsSet labels.Set) (*v1.PodList, error) {
	formatted := labels.FormatLabels(labelsSet)
	return in.k8s.CoreV1().Pods(namespace).List(meta_v1.ListOptions{LabelSelector: formatted})
}

// GetServiceDetails returns full details for a given service, consisting on service description, endpoints and pods.
// A service is defined by the namespace and the service name.
// It returns an error on any problem.
func (in *IstioClient) GetServiceDetails(namespace string, serviceName string) (*ServiceDetails, error) {
	endpointsChan := make(chan endpointsResponse)
	autoscalersChan := make(chan autoscalersResponse)
	podsChan := make(chan podsResponse)

	// Fetch the service first to ensure it exists, then fetch details in parallel
	service, err := in.GetService(namespace, serviceName)
	if err != nil {
		return nil, err
	}

	go func() {
		endpoints, err := in.k8s.CoreV1().Endpoints(namespace).Get(serviceName, emptyGetOptions)
		endpointsChan <- endpointsResponse{endpoints: endpoints, err: err}
	}()

	go func() {
		autoscalers, err := in.k8s.AutoscalingV1().HorizontalPodAutoscalers(namespace).List(emptyListOptions)
		autoscalersChan <- autoscalersResponse{autoscalers: autoscalers, err: err}
	}()

	go func() {
		pods, err := in.GetPods(namespace, service.Spec.Selector)
		podsChan <- podsResponse{pods: pods, err: err}
	}()

	// Last fetch can be performed in main thread. This list is potentially too large and will be narrowed down below
	deployments, err := in.k8s.AppsV1beta1().Deployments(namespace).List(emptyListOptions)
	if err != nil {
		return nil, err
	}

	serviceDetails := ServiceDetails{}

	serviceDetails.Service = service

	endpointsResponse := <-endpointsChan
	if endpointsResponse.err != nil {
		return nil, endpointsResponse.err
	}
	serviceDetails.Endpoints = endpointsResponse.endpoints

	autoscalersResponse := <-autoscalersChan
	if autoscalersResponse.err != nil {
		return nil, autoscalersResponse.err
	}
	serviceDetails.Autoscalers = autoscalersResponse.autoscalers

	podsResponse := <-podsChan
	if podsResponse.err != nil {
		return nil, podsResponse.err
	}
	serviceDetails.Pods = podsResponse.pods

	// Finally, after we get the pods we can narrow down the deployments list
	serviceDetails.Deployments = &v1beta1.DeploymentList{
		Items: FilterDeploymentsForService(service, podsResponse.pods, deployments)}

	return &serviceDetails, nil
}

// GetServicePods returns the list of pods associated to a given service. namespace is required.
// A selector is generated using the canonical labels for serviceName (required)
// and serviceVersion (optional). An error is returned on any problem.
func (in *IstioClient) GetServicePods(namespace, serviceName, serviceVersion string) (*v1.PodList, error) {
	cfg := config.Get()
	selector := labels.Set{cfg.ServiceFilterLabelName: serviceName}
	if "" != serviceVersion {
		selector[cfg.VersionFilterLabelName] = serviceVersion
	}
	return in.GetPods(namespace, selector)
}

func filterAutoscalersByDeployments(deploymentNames []string, al *autoscalingV1.HorizontalPodAutoscalerList) *autoscalingV1.HorizontalPodAutoscalerList {
	autoscalers := make([]autoscalingV1.HorizontalPodAutoscaler, 0, len(al.Items))

	for _, autoscaler := range al.Items {
		for _, deploymentName := range deploymentNames {
			if deploymentName == autoscaler.Spec.ScaleTargetRef.Name {
				autoscalers = append(autoscalers, autoscaler)
			}
		}
	}

	al.Items = autoscalers
	return al
}

func getDeploymentNames(deployments *v1beta1.DeploymentList) []string {
	deploymentNames := make([]string, len(deployments.Items))
	for _, deployment := range deployments.Items {
		deploymentNames = append(deploymentNames, deployment.Name)
	}

	return deploymentNames
}

func (in *IstioClient) getServiceList(namespace string, servicesChan chan servicesResponse) {
	services, err := in.k8s.CoreV1().Services(namespace).List(emptyListOptions)
	servicesChan <- servicesResponse{services: services, err: err}
}

func (in *IstioClient) getPodsList(namespace string, podsChan chan podsResponse) {
	pods, err := in.k8s.CoreV1().Pods(namespace).List(emptyListOptions)
	podsChan <- podsResponse{pods: pods, err: err}
}

func (in *IstioClient) getDeployments(namespace string, deploymentsChan chan deploymentsResponse) {
	deployments, err := in.k8s.AppsV1beta1().Deployments(namespace).List(emptyListOptions)
	deploymentsChan <- deploymentsResponse{deployments: deployments, err: err}
}
