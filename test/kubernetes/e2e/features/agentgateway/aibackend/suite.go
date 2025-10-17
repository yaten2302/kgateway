//go:build e2e

package aibackend

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/kagent-dev/mockllm"
	"github.com/onsi/gomega"
	"github.com/openai/openai-go"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/fsutils"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/kubeutils"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/requestutils/curl"
	testmatchers "github.com/kgateway-dev/kgateway/v2/test/gomega/matchers"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e"
	testdefaults "github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/defaults"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/tests/base"
)

const (
	// Ref: https://github.com/agentgateway/agentgateway/blob/0eff44a748b80030141ebe1d3626c780d05b0265/crates/agentgateway/src/llm/policy.rs#L502
	agwDefaultPromptGuardResponse = "The request was rejected due to inappropriate content"

	// Ref: https://github.com/solo-io/gloo-gateway-use-cases/blob/76e6cec2f0b41eda7a93ac87a1b0f41ddb17503c/ai-guardrail-webhook-server/main.py#L105
	guardrailsWebhookBlockResponse = "request blocked"

	// Ref: https://github.com/solo-io/gloo-gateway-use-cases/blob/76e6cec2f0b41eda7a93ac87a1b0f41ddb17503c/ai-guardrail-webhook-server/main.py#L112
	maskedPatternResponse = "****ing"

	dockerBridgeIfaceIP = "172.17.0.1"

	macOSDockerBridgeHost = "host.docker.internal"
)

var _ e2e.NewSuiteFunc = NewTestingSuite

var (
	// manifests
	setupManifest = filepath.Join(fsutils.MustGetThisDir(), "testdata", "setup.yaml")

	// objects
	gatewayObjectMeta = metav1.ObjectMeta{
		Name:      "ai-gateway",
		Namespace: "default",
	}

	// test cases
	setup = base.TestCase{
		Manifests: []string{
			testdefaults.CurlPodManifest,
			testdefaults.AIGuardrailsWebhookManifest,
		},
		ManifestsWithTransform: map[string]func(string) string{
			setupManifest: func(original string) string {
				if runtime.GOOS == "darwin" {
					return strings.ReplaceAll(original, dockerBridgeIfaceIP, macOSDockerBridgeHost)
				}
				return original
			},
		},
	}
	testCases = map[string]*base.TestCase{}
)

// testingSuite is a suite of basic AI backend tests
type testingSuite struct {
	*base.BaseTestingSuite
}

func NewTestingSuite(ctx context.Context, testInst *e2e.TestInstallation) suite.TestingSuite {
	return &testingSuite{
		base.NewBaseTestingSuite(ctx, testInst, setup, testCases),
	}
}

func (s *testingSuite) TestRouting() {
	server := s.NewMockReqRespServer(MockReqResp{
		Req:  "What is the name of this project?",
		Resp: "The name of this project is kgateway",
	})

	s.TestInstallation.Assertions.AssertEventualCurlResponse(
		s.T().Context(),
		testdefaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithHost(kubeutils.ServiceFQDN(gatewayObjectMeta)),
			curl.WithPort(8080),
			curl.WithPath("/v1/chat/completions"),
			curl.WithPostBody(`{"messages": [{"role": "user", "content": "What is the name of this project?"}]}`),
			curl.WithHeader("Content-Type", "application/json"),
		},
		&testmatchers.HttpResponse{
			StatusCode: http.StatusOK,
			Body:       gomega.ContainSubstring(`The name of this project is kgateway`),
		},
		5*time.Second,
	)

	s.Require().NoError(server.Stop(s.T().Context()))
}

func (s *testingSuite) TestPromptGuard() {
	server := s.NewMockReqRespServer(
		MockReqResp{
			Req:  "Return an example credit card number",
			Resp: "4111-1111-1111-1111 is an example credit card number",
		},
		MockReqResp{
			Req:  "Return an example SSN",
			Resp: "123-45-6789 is an example SSN",
		},
	)

	// Test request guard
	s.TestInstallation.Assertions.AssertEventualCurlResponse(
		s.T().Context(),
		testdefaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithHost(kubeutils.ServiceFQDN(gatewayObjectMeta)),
			curl.WithPort(8080),
			curl.WithPath("/v1/chat/completions"),
			curl.WithPostBody(`{"messages": [{"role": "user", "content": "Return an example credit card number"}]}`),
			curl.WithHeader("Content-Type", "application/json"),
		},
		&testmatchers.HttpResponse{
			StatusCode: http.StatusForbidden,
			Body:       gomega.ContainSubstring(`request rejected`),
		},
		5*time.Second,
	)

	// Test response guard
	s.TestInstallation.Assertions.AssertEventualCurlResponse(
		s.T().Context(),
		testdefaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithHost(kubeutils.ServiceFQDN(gatewayObjectMeta)),
			curl.WithPort(8080),
			curl.WithPath("/v1/chat/completions"),
			curl.WithPostBody(`{"messages": [{"role": "user", "content": "Return an example SSN"}]}`),
			curl.WithHeader("Content-Type", "application/json"),
		},
		&testmatchers.HttpResponse{
			StatusCode: http.StatusForbidden,
			Body:       gomega.ContainSubstring(agwDefaultPromptGuardResponse),
		},
		5*time.Second,
	)

	s.Require().NoError(server.Stop(s.T().Context()))
}

func (s *testingSuite) TestWebhook() {
	server := s.NewMockReqRespServer(
		MockReqResp{
			Provider: MockProviderAnthropic,
			Req:      "return blocked content",
			// Resp is not relevant as the request will be blocked by the webhook
		},
		MockReqResp{
			Provider: MockProviderAnthropic,
			Req:      "Explain data masking",
			Resp:     "Data masking is the process of hiding sensitive information by redacting or obfuscating it",
		},
	)

	// Ensure the guardrails webhook Pod is running and ready before sending traffic
	s.TestInstallation.Assertions.EventuallyPodsRunning(
		s.T().Context(),
		"default",
		metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=ai-guardrails-webhook"},
		30*time.Second,
	)

	// Test request webhook
	s.TestInstallation.Assertions.AssertEventualCurlResponse(
		s.T().Context(),
		testdefaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithHost(kubeutils.ServiceFQDN(gatewayObjectMeta)),
			curl.WithPort(8080),
			curl.WithPath("/v1/messages"),
			curl.WithPostBody(`{"messages": [{"role": "user", "content": "return blocked content"}]}`),
			curl.WithHeaders(map[string]string{
				"Content-Type": "application/json",
				"x-direction":  "request", // matches request webhook route
				// below headers are required due to https://github.com/agentgateway/agentgateway/issues/509
				"x-api-key":         "fake",
				"anthropic-version": "fake",
			}),
		},
		&testmatchers.HttpResponse{
			StatusCode: http.StatusForbidden,
			Body:       gomega.ContainSubstring(guardrailsWebhookBlockResponse),
		},
		30*time.Second,
	)

	// Test response webhook
	s.TestInstallation.Assertions.AssertEventualCurlResponse(
		s.T().Context(),
		testdefaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithHost(kubeutils.ServiceFQDN(gatewayObjectMeta)),
			curl.WithPort(8080),
			curl.WithPath("/v1/messages"),
			curl.WithPostBody(`{"messages": [{"role": "user", "content": "Explain data masking"}]}`),
			curl.WithHeaders(map[string]string{
				"Content-Type": "application/json",
				"x-direction":  "response", // matches response webhook route
				// below headers are required due to https://github.com/agentgateway/agentgateway/issues/509
				"x-api-key":         "fake",
				"anthropic-version": "fake",
			}),
		},
		&testmatchers.HttpResponse{
			StatusCode: http.StatusOK,
			Body:       gomega.ContainSubstring(maskedPatternResponse),
		},
		30*time.Second,
	)

	s.Require().NoError(server.Stop(s.T().Context()))
}

type MockProvider string

const (
	MockProviderOpenAI    MockProvider = "openai"
	MockProviderAnthropic MockProvider = "anthropic"
)

type MockReqResp struct {
	Req      string
	Resp     string
	Provider MockProvider // defaults to OpenAI
}

// NewMockReqRespServer creates and starts a new mock LLM server that responds with `resp` when it receives a request containing `req`.
// The caller must stop the server with server.Stop()
func (s *testingSuite) NewMockReqRespServer(mocks ...MockReqResp) *mockllm.Server {
	var mOpenAI []mockllm.OpenAIMock
	var mAnthropic []mockllm.AnthropicMock

	for _, mr := range mocks {
		switch mr.Provider {
		case MockProviderAnthropic:
			anthropicRequest := anthropic.MessageNewParams{
				Model:     "claude-3-5-sonnet-20240620",
				MaxTokens: 1000,
				Messages: []anthropic.MessageParam{
					{
						Role: anthropic.MessageParamRoleUser,
						Content: []anthropic.ContentBlockParamUnion{
							{
								OfText: &anthropic.TextBlockParam{
									Text: mr.Req,
								},
							},
						},
					},
				},
			}

			anthropicResponse := anthropic.Message{
				ID:   "msg_123",
				Type: "message",
				Role: "assistant",
				Content: []anthropic.ContentBlockUnion{
					{
						Type: "text",
						Text: mr.Resp,
					},
				},
				Model:      "claude-3-5-sonnet-20240620",
				StopReason: "end_turn",
			}

			// Convert to JSON and back to get SDK-compatible structure
			var mock mockllm.AnthropicMock
			mock.Name = string(mr.Provider) + ": " + mr.Req
			mock.Response = anthropicResponse
			mock.Match = mockllm.AnthropicRequestMatch{
				MatchType: mockllm.MatchTypeExact,
				Message:   anthropicRequest.Messages[len(anthropicRequest.Messages)-1],
			}
			// Marshal and unmarshal the request to get it in the right format
			reqBytes, err := json.Marshal(anthropicRequest)
			s.Require().NoError(err)
			err = json.Unmarshal(reqBytes, &mock.Match)
			s.Require().NoError(err)

			mAnthropic = append(mAnthropic, mock)

		case MockProviderOpenAI:
			fallthrough
		default:
			openaiRequest := openai.ChatCompletionNewParams{
				Model: "gpt-4o-mini",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role: "user",
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfString: openai.String(mr.Req),
							},
						},
					},
				},
			}

			openaiResponse := openai.ChatCompletion{
				ID:      "chatcmpl-123",
				Object:  "chat.completion",
				Created: 1677652288,
				Model:   "gpt-4o-mini",
				Choices: []openai.ChatCompletionChoice{
					{
						Index: 0,
						Message: openai.ChatCompletionMessage{
							Role:    "assistant",
							Content: mr.Resp,
						},
						FinishReason: "stop",
					},
				},
				ServiceTier: openai.ChatCompletionServiceTierDefault, // required to avoid Agw parse error with an empty value
			}

			// Convert to JSON and back to get SDK-compatible structure
			var mock mockllm.OpenAIMock
			mock.Name = string(mr.Provider) + ": " + mr.Req
			mock.Response = openaiResponse
			mock.Match = mockllm.OpenAIRequestMatch{
				MatchType: mockllm.MatchTypeExact,
				Message:   openaiRequest.Messages[len(openaiRequest.Messages)-1],
			}

			// Marshal and unmarshal the request to get it in the right format
			reqBytes, err := json.Marshal(openaiRequest)
			s.Require().NoError(err)
			s.T().Logf("Request: %s", string(reqBytes))
			err = json.Unmarshal(reqBytes, &mock.Match)
			s.Require().NoError(err)

			mOpenAI = append(mOpenAI, mock)
		}
	}

	config := mockllm.Config{
		OpenAI:     mOpenAI,
		Anthropic:  mAnthropic,
		ListenAddr: "0.0.0.0:9234",
	}

	// Start server
	server := mockllm.NewServer(config)
	baseURL, err := server.Start(s.T().Context())
	s.T().Logf("Started mock LLM server at %s", baseURL)
	s.Require().NoError(err)

	return server
}
