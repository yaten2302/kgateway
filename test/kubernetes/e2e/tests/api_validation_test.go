//go:build e2e

package tests

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/testutils/install"
)

func TestAPIValidation(t *testing.T) {
	ctx := t.Context()
	ti := e2e.CreateTestInstallation(t, &install.Context{
		ValuesManifestFile:        e2e.EmptyValuesManifestPath,
		ProfileValuesManifestFile: e2e.CommonRecommendationManifest,
		InstallNamespace:          "kgateway-system",
	})

	tests := []struct {
		name       string
		input      string
		wantErrors []string
	}{
		{
			name: "Backend: enforce ExactlyOneOf for backend type",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: Backend
metadata:
  name: backend-oneof
spec:
  type: AWS
  aws:
    accountId: "000000000000"
    lambda:
      functionName: hello-function
      invocationMode: Async
  static:
    hosts:
    - host: example.com
      port: 80
`,
			wantErrors: []string{"exactly one of the fields in [ai aws static dynamicForwardProxy mcp] must be set"},
		},
		{
			name: "Backend: empty lambda qualifier does not match pattern",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: Backend
metadata:
  name: backend-empty-lambda-qualifier
spec:
  type: AWS
  aws:
    accountId: "000000000000"
    lambda:
      functionName: hello-function
      qualifier: ""
`,
			wantErrors: []string{"spec.aws.lambda.qualifier in body should match "},
		},
		{
			name: "BackendConfigPolicy: enforce AtMostOneOf for HTTP protocol options",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-both-http-options
spec:
  targetRefs:
  - group: ""
    kind: Service
    name: test-service
  http1ProtocolOptions:
    enableTrailers: true
  http2ProtocolOptions:
    maxConcurrentStreams: 100
    overrideStreamErrorOnInvalidHttpMessage: true
`,
			wantErrors: []string{"at most one of the fields in [http1ProtocolOptions http2ProtocolOptions] may be set"},
		},
		{
			name: "BackendConfigPolicy: HTTP2 protocol options with integer values",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-http2-integers
spec:
  targetRefs:
  - group: ""
    kind: Service
    name: test-service
  http2ProtocolOptions:
    initialConnectionWindowSize: 65535
    initialStreamWindowSize: 2147483647
    maxConcurrentStreams: 100
`,
		},
		{
			name: "BackendConfigPolicy: HTTP2 protocol options with string values",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-http2-strings
spec:
  targetRefs:
  - group: ""
    kind: Service
    name: test-service
  http2ProtocolOptions:
    initialConnectionWindowSize: "65535"
    initialStreamWindowSize: "2147483647"
    maxConcurrentStreams: 100
`,
		},
		{
			name: "BackendConfigPolicy: HTTP2 protocol options with invalid integer values",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-http2-invalid-integers
spec:
  targetRefs:
  - group: ""
    kind: Service
    name: test-service
  http2ProtocolOptions:
    initialConnectionWindowSize: 1000
    initialStreamWindowSize: 2147483648
`,
			wantErrors: []string{"InitialConnectionWindowSize must be between 65535 and 2147483647 bytes (inclusive)"},
		},
		{
			name: "BackendConfigPolicy: valid target references",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-valid-targets
spec:
  targetRefs:
  - group: ""
    kind: Service
    name: test-service
  - group: gateway.kgateway.dev
    kind: Backend
    name: test-backend
  targetSelectors:
  - group: ""
    kind: Service
    matchLabels:
      app: myapp
  http1ProtocolOptions:
    enableTrailers: true
`,
		},
		{
			name: "BackendConfigPolicy: invalid target reference",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-invalid-target
spec:
  targetRefs:
  - group: apps
    kind: Deployment
    name: test-deployment
`,
			wantErrors: []string{"TargetRefs must reference either a Kubernetes Service or a Backend API"},
		},
		{
			name: "BackendConfigPolicy: invalid target selector",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-invalid-selector
spec:
  targetSelectors:
  - group: apps
    kind: Deployment
    matchLabels:
      app: myapp
`,
			wantErrors: []string{"TargetSelectors must reference either a Kubernetes Service or a Backend API"},
		},
		{
			name: "BackendConfigPolicy: invalid aggression",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-invalid-aggression
spec:
  targetRefs:
  - group: ""
    kind: Service
    name: test-service
  loadBalancer:
    roundRobin:
      slowStart:
        window: 10s
        aggression: ""
        minWeightPercent: 10
`,
			wantErrors: []string{"Aggression, if specified, must be a string representing a number greater than 0.0"},
		},
		{
			name: "BackendConfigPolicy: invalid durations",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: BackendConfigPolicy
metadata:
  name: backend-config-invalid-duration
spec:
  connectTimeout: -1s
  commonHttpProtocolOptions:
    idleTimeout: 1x
    maxStreamDuration: abc
  tcpKeepalive:
    keepAliveTime: 0s
    keepAliveInterval: "0"
  healthCheck:
    timeout: a
    interval: b
    unhealthyThreshold: 3
    healthyThreshold: 2
    http:
      path: /healthz
      host: example.com
      method: HEAD
  loadBalancer:
    updateMergeWindow: z
    roundRobin:
      slowStart:
        window: 10s
`,
			wantErrors: []string{
				"spec.commonHttpProtocolOptions.idleTimeout: Invalid value: \"string\": invalid duration value",
				"spec.commonHttpProtocolOptions.maxStreamDuration: Invalid value: \"string\": invalid duration value",
				"spec.connectTimeout: Invalid value: \"string\": invalid duration value",
				"spec.healthCheck.interval: Invalid value: \"string\": invalid duration value",
				"spec.healthCheck.timeout: Invalid value: \"string\": invalid duration value",
				"spec.loadBalancer.updateMergeWindow: Invalid value: \"string\": invalid duration value",
				"spec.tcpKeepalive.keepAliveInterval: Invalid value: \"string\": invalid duration value",
				"spec.tcpKeepalive.keepAliveInterval: Invalid value: \"string\": keepAliveInterval must be at least 1 second",
				"spec.tcpKeepalive.keepAliveTime: Invalid value: \"string\": keepAliveTime must be at least 1 second",
			},
		},
		{
			name: "TrafficPolicy: valid target references",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: traffic-policy-valid-targets
spec:
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: test-gateway
  - group: gateway.networking.k8s.io
    kind: HTTPRoute
    name: test-route
  - group: gateway.networking.x-k8s.io
    kind: XListenerSet
    name: test-listener
  targetSelectors:
  - group: gateway.networking.k8s.io
    kind: Gateway
    matchLabels:
      app: myapp
`,
		},
		{
			name: "TrafficPolicy: invalid target reference",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: traffic-policy-invalid-target
spec:
  targetRefs:
  - group: apps
    kind: Deployment
    name: test-deployment
`,
			wantErrors: []string{"targetRefs may only reference Gateway, HTTPRoute, XListenerSet, or Backend resources"},
		},
		{
			name: "TrafficPolicy: policy with autoHostRewrite can only target HTTPRoute",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: traffic-policy-ahr-invalid-target
spec:
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: test-gateway
  autoHostRewrite: true
`,
			wantErrors: []string{"autoHostRewrite can only be used when targeting HTTPRoute resources"},
		},
		{
			name: "HTTPListenerPolicy: valid target references",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: HTTPListenerPolicy
metadata:
  name: http-listener-policy-valid-targets
spec:
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: test-gateway
  targetSelectors:
  - group: gateway.networking.k8s.io
    kind: Gateway
    matchLabels:
      app: myapp
`,
		},
		{
			name: "HTTPListenerPolicy: invalid target reference - HTTPRoute not allowed",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: HTTPListenerPolicy
metadata:
  name: http-listener-policy-invalid-target-httproute
spec:
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: HTTPRoute
    name: test-route
`,
			wantErrors: []string{"targetRefs may only reference Gateway resources"},
		},
		{
			name: "HTTPListenerPolicy: invalid target reference - wrong resource type",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: HTTPListenerPolicy
metadata:
  name: http-listener-policy-invalid-target
spec:
  targetRefs:
  - group: gateway.networking.x-k8s.io
    kind: XListenerSet
    name: test-listener
`,
			wantErrors: []string{"targetRefs may only reference Gateway resources"},
		},
		{
			name: "DirectResponse: empty body not allowed",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: DirectResponse
metadata:
  name: directresponse
spec:
  status: 200
  body: ""
`,
			wantErrors: []string{"spec.body in body should be at least 1 chars long"},
		},
		{
			name: "TrafficPolicy: empty generic key and value in rate limit descriptor",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: traffic-policy-empty-generic-fields
spec:
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: HTTPRoute
    name: test-route
  rateLimit:
    global:
      descriptors:
      - entries:
        - type: Generic
          generic:
            key: ""
            value: ""
      extensionRef:
        name: test-extension
`,
			wantErrors: []string{
				"spec.rateLimit.global.descriptors[0].entries[0].generic.key in body should be at least 1 chars long",
				"spec.rateLimit.global.descriptors[0].entries[0].generic.value in body should be at least 1 chars long",
			},
		},
		{
			name: "TrafficPolicy: valid retry and timeouts",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  retry:
    retryOn:
    - gateway-error
    - connect-failure
    - reset
    attempts: 2
    perTryTimeout: 2s
    backoffBaseInterval: 50ms
  timeouts:
    request: 5s
    streamIdle: 60s
`,
		},
		{
			name: "TrafficPolicy: retry.retryOn unset",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  retry:
    attempts: 2
    perTryTimeout: 2s
`,
			wantErrors: []string{"retryOn or statusCodes must be set"},
		},
		{
			name: "TrafficPolicy: retry.perTryTimeout must be lesser than timeouts.request",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  retry:
    retryOn:
    - gateway-error
    - connect-failure
    - reset
    attempts: 2
    perTryTimeout: 6s
  timeouts:
    request: 5s
    streamIdle: 60s
`,
			wantErrors: []string{"retry.perTryTimeout must be lesser than timeouts.request"},
		},
		{
			name: "TrafficPolicy: retry.perTryTimeout must be at least 1ms",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  retry:
    retryOn:
    - gateway-error
    - connect-failure
    - reset
    attempts: 2
    perTryTimeout: 0.1ms
`,
			wantErrors: []string{"perTryTimeout must be at least 1ms"},
		},
		{
			name: "TrafficPolicy: retry.perTryTimeout must be a valid duration value",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  retry:
    retryOn:
    - gateway-error
    - connect-failure
    - reset
    perTryTimeout: 1f
`,
			wantErrors: []string{
				"spec.retry.perTryTimeout: Invalid value: \"string\": invalid duration value",
				"spec.retry.perTryTimeout: Invalid value: \"string\": type conversion error from 'string' to 'google.protobuf.Duration' evaluating rule: retry.perTryTimeout must be at least 1ms",
			},
		},
		{
			name: "TrafficPolicy: targetRefs[].sectionName must be set when targeting Gateway resources with retry policy",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: test
  retry:
    retryOn:
    - gateway-error
`,
			wantErrors: []string{
				"targetRefs[].sectionName must be set when targeting Gateway resources with retry policy",
			},
		},
		{
			name: "TrafficPolicy: targetSelectors[].sectionName must be set when targeting Gateway resources with retry policy",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  targetSelectors:
  - group: gateway.networking.k8s.io
    kind: Gateway
    matchLabels:
      foo: bar
  retry:
    retryOn:
    - gateway-error
`,
			wantErrors: []string{
				"targetSelectors[].sectionName must be set when targeting Gateway resources with retry policy",
			},
		},
		{
			name: "TrafficPolicy: timeouts.request must be a valid duration value",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  timeouts:
    request: foo
`,
			wantErrors: []string{
				"spec.timeouts.request: Invalid value: \"string\": invalid duration value",
			},
		},
		{
			name: "TrafficPolicy: timeouts.streamIdle must be a valid duration value",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  timeouts:
    streamIdle: -1s
`,
			wantErrors: []string{
				"spec.timeouts.streamIdle: Invalid value: \"string\": invalid duration value",
			},
		},
		{
			name: "TrafficPolicy Buffer maxRequestSize with integer",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  buffer:
    maxRequestSize: 65536
`,
		},
		{
			name: "TrafficPolicy Buffer maxRequestSize with string",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  buffer:
    maxRequestSize: 64Ki
`,
		},
		{
			name: "TrafficPolicy Buffer maxRequestSize with invalid value",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: test
spec:
  buffer:
    maxRequestSize: 4Gi
`,
			wantErrors: []string{"maxRequestSize must be greater than 0 and less than 4Gi"},
		},
		{
			name: "ProxyDeployment: Strategy is fully fleshed out",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: GatewayParameters
metadata:
  name: test-proxy-deployment-empty
spec:
  kube:
    deployment:
      strategy:
        type: RollingUpdate
        rollingUpdate:
          maxSurge: 100%
          maxUnavailable: 1
`,
		},
		{
			name: "ProxyDeployment: Strategy sets maxSurge and uses implicit type RollingUpdate",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: GatewayParameters
metadata:
  name: test-proxy-deployment-maxsurge
spec:
  kube:
    deployment:
      strategy:
        rollingUpdate:
          maxSurge: 100%
`,
		},
		{
			name: "ProxyDeployment: Strategy has an empty rollingUpdate override",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: GatewayParameters
metadata:
  name: test-proxy-deployment-rollingupdate-empty
spec:
  kube:
    deployment:
      strategy:
        rollingUpdate: {}
`,
		},
		{
			name: "ProxyDeployment: Strategy Recreate",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: GatewayParameters
metadata:
  name: test-proxy-deployment-recreate
spec:
  kube:
    deployment:
      strategy:
        type: Recreate
`,
		},
		{
			name: "ProxyDeployment: Strategy has an unknown rollout type and acts in a forwards-compatible fashion",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: GatewayParameters
metadata:
  name: test-proxy-deployment-unknownstrategem
spec:
  kube:
    deployment:
      strategy:
        type: SomeStrategemIntroducedInTheFuture
`,
		},
		{
			name: "MCP backend selector requires namespace|service to be set",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: Backend
metadata:
  name: mcp-backend
spec:
  type: MCP
  mcp:
    targets:
    - name: mcp-app
      selector: {}
`,
			wantErrors: []string{`spec.mcp.targets[0].selector: Invalid value: "object": at least one of namespace or service must be set`},
		},
		{
			name: "MCP backend namespace selector resolves to the reserved CEL keyword __namespace__",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: Backend
metadata:
  name: mcp-backend
spec:
  type: MCP
  mcp:
    targets:
    - name: mcp-app
      selector:
        namespace:
          matchLabels:
            app: mcp-app
`,
		},
		{
			name: "AI priorityGroups with no overlapping provider names",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: Backend
metadata:
  name: no-overlapping-names
spec:
  type: AI
  ai:
    priorityGroups:
    - providers:
      - name: first
        openai:
          model: "gpt-4o"
          authToken:
            kind: "SecretRef"
            secretRef:
              name: openai-primary-secret
      - name: second
        anthropic:
          model: "claude-3-opus-20240229"
          authToken:
            kind: "Inline"
            inline: "sk-anthropic-primary"
    - providers:
      - name: third
        openai:
          model: "gpt-4o"
          authToken:
            kind: "SecretRef"
            secretRef:
              name: openai-primary-secret
`,
		},
		{
			name: "AI priorityGroups with overlapping provider names within a group",
			input: `---
apiVersion: gateway.kgateway.dev/v1alpha1
kind: Backend
metadata:
  name: same-group-overlap
spec:
  type: AI
  ai:
    priorityGroups:
    - providers:
      - name: first
        openai:
          model: "gpt-4o"
          authToken:
            kind: "SecretRef"
            secretRef:
              name: openai-primary-secret
      - name: first
        anthropic:
          model: "claude-3-opus-20240229"
          authToken:
            kind: "Inline"
            inline: "sk-anthropic-primary"
    - providers:
      - name: third
        openai:
          model: "gpt-4o"
          authToken:
            kind: "SecretRef"
            secretRef:
              name: openai-primary-secret
`,
			wantErrors: []string{`spec.ai.priorityGroups[0].providers: Invalid value: "array": provider names must be unique within a group`},
		},
		/* Test is disabled since the CEL rule is disabled to support older k8s versions
				{
					name: "AI priorityGroups with overlapping provider names across groups",
					input: `---
		apiVersion: gateway.kgateway.dev/v1alpha1
		kind: Backend
		metadata:
		  name: different-group-overlap
		spec:
		  type: AI
		  ai:
		    priorityGroups:
		    - providers:
		      - name: first
		        openai:
		          model: "gpt-4o"
		          authToken:
		            kind: "SecretRef"
		            secretRef:
		              name: openai-primary-secret
		      - name: second
		        anthropic:
		          model: "claude-3-opus-20240229"
		          authToken:
		            kind: "Inline"
		            inline: "sk-anthropic-primary"
		    - providers:
		      - name: first
		        openai:
		          model: "gpt-4o"
		          authToken:
		            kind: "SecretRef"
		            secretRef:
		              name: openai-primary-secret
		`,
					wantErrors: []string{`spec.ai.priorityGroups: Invalid value: "array": provider names must be unique across groups`},
				},
		*/
	}

	t.Cleanup(func() {
		ctx := context.Background()
		ti.UninstallKgatewayCRDs(ctx)
	})
	ti.InstallKgatewayCRDsFromLocalChart(ctx)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			t.Cleanup(func() {
				ti.Actions.Kubectl().DeleteFile(ctx, tc.input) //nolint:errcheck
			})

			out := new(bytes.Buffer)

			err := ti.Actions.Kubectl().WithReceiver(out).Apply(ctx, []byte(tc.input))
			if len(tc.wantErrors) > 0 {
				r.Error(err)
				for _, wantErr := range tc.wantErrors {
					r.Contains(out.String(), wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("kubectl apply failed with output: %s", out.String())
				}
				r.NoError(err)
			}
		})
	}
}
