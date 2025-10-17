package deployer

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kgateway-dev/kgateway/v2/pkg/deployer"
)

func NewGatewayDeployer(controllerName, agwControllerName, agwGatewayClassName string, cli client.Client, gwParams *GatewayParameters) (*deployer.Deployer, error) {
	envoyChart, err := LoadEnvoyChart()
	if err != nil {
		return nil, err
	}
	agentgatewayChart, err := LoadAgentgatewayChart()
	if err != nil {
		return nil, err
	}
	return deployer.NewDeployerWithMultipleCharts(
		controllerName, agwControllerName, agwGatewayClassName, cli, envoyChart, agentgatewayChart, gwParams, GatewayReleaseNameAndNamespace), nil
}

func NewInferencePoolDeployer(controllerName, agwControllerName, agwGatewayClassName string, cli client.Client) (*deployer.Deployer, error) {
	inferenceExt := &InferenceExtension{}
	chart, err := LoadInferencePoolChart()
	if err != nil {
		return nil, err
	}
	return deployer.NewDeployer(
		controllerName, agwControllerName, agwGatewayClassName, cli, chart, inferenceExt, InferenceExtensionReleaseNameAndNamespace), nil
}
