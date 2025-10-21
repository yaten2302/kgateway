package trafficpolicy

import (
	"errors"
	"fmt"

	xdscorev3 "github.com/cncf/xds/go/xds/core/v3"
	xdsmatcherv3 "github.com/cncf/xds/go/xds/type/matcher/v3"
	envoycorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/config/ratelimit/v3"
	envoymatchingv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/matching/v3"
	envoycompositev3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/composite/v3"
	envoy_ext_authz_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_authz/v3"
	envoyextprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	ratev3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ratelimit/v3"
	envoynetworkv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/matching/common_inputs/network/v3"
	envoymetadatav3 "github.com/envoyproxy/go-control-plane/envoy/extensions/matching/input_matchers/metadata/v3"
	envoymatcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	envoytypev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"istio.io/istio/pkg/kube/krt"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/krtcollections"
	"github.com/kgateway-dev/kgateway/v2/internal/kgateway/utils"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/collections"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
)

type TrafficPolicyGatewayExtensionIR struct {
	Name             string
	ExtType          v1alpha1.GatewayExtensionType
	ExtAuth          *envoy_ext_authz_v3.ExtAuthz
	ExtProc          *envoymatchingv3.ExtensionWithMatcher
	RateLimit        *ratev3.RateLimit
	PrecedenceWeight int32
	Err              error
}

// ResourceName returns the unique name for this extension.
func (e TrafficPolicyGatewayExtensionIR) ResourceName() string {
	return e.Name
}

func (e TrafficPolicyGatewayExtensionIR) Equals(other TrafficPolicyGatewayExtensionIR) bool {
	if e.ExtType != other.ExtType {
		return false
	}

	if !proto.Equal(e.ExtAuth, other.ExtAuth) {
		return false
	}
	if !proto.Equal(e.ExtProc, other.ExtProc) {
		return false
	}
	if !proto.Equal(e.RateLimit, other.RateLimit) {
		return false
	}
	if e.PrecedenceWeight != other.PrecedenceWeight {
		return false
	}

	if e.Err == nil && other.Err != nil {
		return false
	}
	if e.Err != nil && other.Err == nil {
		return false
	}
	if (e.Err != nil && other.Err != nil) && e.Err.Error() != other.Err.Error() {
		return false
	}

	return true
}

// Validate performs PGV-based validation on the gateway extension components
func (e TrafficPolicyGatewayExtensionIR) Validate() error {
	if e.Err != nil {
		// If there's an error in the IR, validation doesn't make sense.
		return nil
	}
	if e.ExtAuth != nil {
		if err := e.ExtAuth.ValidateAll(); err != nil {
			return err
		}
	}
	if e.ExtProc != nil {
		if err := e.ExtProc.ValidateAll(); err != nil {
			return err
		}
	}
	if e.RateLimit != nil {
		if err := e.RateLimit.ValidateAll(); err != nil {
			return err
		}
	}
	return nil
}

func TranslateGatewayExtensionBuilder(commoncol *collections.CommonCollections) func(krtctx krt.HandlerContext, gExt ir.GatewayExtension) *TrafficPolicyGatewayExtensionIR {
	return func(krtctx krt.HandlerContext, gExt ir.GatewayExtension) *TrafficPolicyGatewayExtensionIR {
		p := &TrafficPolicyGatewayExtensionIR{
			Name:             krt.Named{Name: gExt.Name, Namespace: gExt.Namespace}.ResourceName(),
			ExtType:          gExt.Type,
			PrecedenceWeight: gExt.PrecedenceWeight,
		}

		switch gExt.Type {
		case v1alpha1.GatewayExtensionTypeExtAuth:
			envoyGrpcService, err := ResolveExtGrpcService(krtctx, commoncol.BackendIndex, false, gExt.ObjectSource, gExt.ExtAuth.GrpcService)
			if err != nil {
				// TODO: should this be a warning, and set cluster to blackhole?
				p.Err = fmt.Errorf("failed to resolve ExtAuth backend: %w", err)
				return p
			}

			p.ExtAuth = &envoy_ext_authz_v3.ExtAuthz{
				Services: &envoy_ext_authz_v3.ExtAuthz_GrpcService{
					GrpcService: envoyGrpcService,
				},
				FilterEnabledMetadata: ExtAuthzEnabledMetadataMatcher,
				FailureModeAllow:      gExt.ExtAuth.FailOpen,
				ClearRouteCache:       gExt.ExtAuth.ClearRouteCache,
				StatusOnError:         &envoytypev3.HttpStatus{Code: envoytypev3.StatusCode(gExt.ExtAuth.StatusOnError)}, //nolint:gosec // G115: StatusOnError is HTTP status code, valid range fits in int32
			}

			if gExt.ExtAuth.WithRequestBody != nil {
				p.ExtAuth.WithRequestBody = &envoy_ext_authz_v3.BufferSettings{
					MaxRequestBytes:     uint32(gExt.ExtAuth.WithRequestBody.MaxRequestBytes), // nolint:gosec // G115: kubebuilder validation ensures safe for uint32
					AllowPartialMessage: gExt.ExtAuth.WithRequestBody.AllowPartialMessage,
					PackAsBytes:         gExt.ExtAuth.WithRequestBody.PackAsBytes,
				}
			}
			if gExt.ExtAuth.StatPrefix != nil {
				p.ExtAuth.StatPrefix = *gExt.ExtAuth.StatPrefix
			}

		case v1alpha1.GatewayExtensionTypeExtProc:
			envoyGrpcService, err := ResolveExtGrpcService(krtctx, commoncol.BackendIndex, false, gExt.ObjectSource, gExt.ExtProc.GrpcService)
			if err != nil {
				p.Err = fmt.Errorf("failed to resolve ExtProc backend: %w", err)
				return p
			}
			p.ExtProc = buildCompositeExtProcFilter(*gExt.ExtProc, envoyGrpcService)

		case v1alpha1.GatewayExtensionTypeRateLimit:
			if gExt.RateLimit == nil {
				p.Err = fmt.Errorf("rate limit extension missing configuration")
				return p
			}

			grpcService, err := ResolveExtGrpcService(krtctx, commoncol.BackendIndex, false, gExt.ObjectSource, gExt.RateLimit.GrpcService)
			if err != nil {
				p.Err = fmt.Errorf("ratelimit: %w", err)
				return p
			}

			// Use the specialized function for rate limit service resolution
			rateLimitConfig := buildRateLimitFilter(grpcService, gExt.RateLimit)

			p.RateLimit = rateLimitConfig
		}
		return p
	}
}

func ResolveExtGrpcService(
	krtctx krt.HandlerContext,
	backends *krtcollections.BackendIndex,
	disableExtensionRefValidation bool,
	objectSource ir.ObjectSource,
	grpcService *v1alpha1.ExtGrpcService,
) (*envoycorev3.GrpcService, error) {
	// defensive checks, both of these fields are required
	if grpcService == nil {
		return nil, errors.New("grpcService not provided")
	}
	if grpcService.BackendRef == nil {
		return nil, errors.New("backend not provided")
	}

	var backend *ir.BackendObjectIR
	var err error
	backendRef := grpcService.BackendRef.BackendObjectReference
	if disableExtensionRefValidation {
		backend, err = backends.GetBackendFromRefWithoutRefGrantValidation(krtctx, objectSource, backendRef)
	} else {
		backend, err = backends.GetBackendFromRef(krtctx, objectSource, backendRef)
	}
	if err != nil {
		return nil, err
	}

	var clusterName string
	if backend != nil {
		clusterName = backend.ClusterName()
	}
	if clusterName == "" {
		return nil, errors.New("backend not found")
	}
	var authority string
	if grpcService.Authority != nil {
		authority = *grpcService.Authority
	}

	envoyGrpcService := &envoycorev3.GrpcService{
		TargetSpecifier: &envoycorev3.GrpcService_EnvoyGrpc_{
			EnvoyGrpc: &envoycorev3.GrpcService_EnvoyGrpc{
				ClusterName: clusterName,
				Authority:   authority,
			},
		},
		RetryPolicy: buildGRPCRetryPolicy(grpcService.Retry),
	}
	if grpcService.RequestTimeout != nil {
		envoyGrpcService.Timeout = durationpb.New(grpcService.RequestTimeout.Duration)
	}
	return envoyGrpcService, nil
}

func buildGRPCRetryPolicy(in *v1alpha1.GRPCRetryPolicy) *envoycorev3.RetryPolicy {
	if in == nil {
		return nil
	}

	p := &envoycorev3.RetryPolicy{
		NumRetries: wrapperspb.UInt32(uint32(in.Attempts)), //nolint: gosec // G115: kubebuilder validation ensures safe conversion
	}
	if in.Backoff != nil {
		p.RetryBackOff = &envoycorev3.BackoffStrategy{
			BaseInterval: durationpb.New(in.Backoff.BaseInterval.Duration),
		}
		if in.Backoff.MaxInterval != nil {
			p.RetryBackOff.MaxInterval = durationpb.New(in.Backoff.MaxInterval.Duration)
		}
	}
	return p
}

// FIXME: Should this live here instead of the global rate limit plugin?
func buildRateLimitFilter(grpcService *envoycorev3.GrpcService, rateLimit *v1alpha1.RateLimitProvider) *ratev3.RateLimit {
	envoyRateLimit := &ratev3.RateLimit{
		Domain:          rateLimit.Domain,
		FailureModeDeny: !rateLimit.FailOpen,
		RateLimitService: &envoyratelimitv3.RateLimitServiceConfig{
			GrpcService:         grpcService,
			TransportApiVersion: envoycorev3.ApiVersion_V3,
		},
		EnableXRatelimitHeaders: convertXRL(rateLimit.XRateLimitHeaders),
	}

	// Set timeout (we expect it always to have a valid value or default due to CRD validation)
	envoyRateLimit.Timeout = durationpb.New(rateLimit.Timeout.Duration)

	return envoyRateLimit
}

func convertXRL(in v1alpha1.XRateLimitHeadersStandard) ratev3.RateLimit_XRateLimitHeadersRFCVersion {
	switch in {
	case v1alpha1.XRateLimitHeaderDraftV03:
		return ratev3.RateLimit_DRAFT_VERSION_03
	case v1alpha1.XRateLimitHeaderOff:
		return ratev3.RateLimit_OFF
	default:
		return ratev3.RateLimit_OFF
	}
}

func convertRCA(in v1alpha1.ExtProcRouteCacheAction) envoyextprocv3.ExternalProcessor_RouteCacheAction {
	switch in {
	case v1alpha1.RouteCacheActionClear:
		return envoyextprocv3.ExternalProcessor_CLEAR
	case v1alpha1.RouteCacheActionRetain:
		return envoyextprocv3.ExternalProcessor_RETAIN
	case v1alpha1.RouteCacheActionFromResponse:
		return envoyextprocv3.ExternalProcessor_DEFAULT
	default:
		return envoyextprocv3.ExternalProcessor_DEFAULT
	}
}

// buildCompositeExtProcFilter builds a composite filter for external processing so that
// the filter can be conditionally disabled with the global_disable/ext_proc filter is enabled
func buildCompositeExtProcFilter(in v1alpha1.ExtProcProvider, envoyGrpcService *envoycorev3.GrpcService) *envoymatchingv3.ExtensionWithMatcher {
	filter := &envoyextprocv3.ExternalProcessor{
		GrpcService:      envoyGrpcService,
		FailureModeAllow: in.FailOpen,
		RouteCacheAction: convertRCA(in.RouteCacheAction),
	}
	if mode := toEnvoyProcessingMode(in.ProcessingMode); mode != nil {
		filter.ProcessingMode = mode
	}
	if in.MessageTimeout != nil {
		filter.MessageTimeout = durationpb.New(in.MessageTimeout.Duration)
	}
	if in.MaxMessageTimeout != nil {
		filter.MaxMessageTimeout = durationpb.New(in.MaxMessageTimeout.Duration)
	}
	if in.StatPrefix != nil {
		filter.StatPrefix = *in.StatPrefix
	}
	if in.MetadataOptions != nil {
		filter.MetadataOptions = &envoyextprocv3.MetadataOptions{}
		if in.MetadataOptions.Forwarding != nil {
			filter.MetadataOptions.ForwardingNamespaces = &envoyextprocv3.MetadataOptions_MetadataNamespaces{
				Typed:   in.MetadataOptions.Forwarding.Typed,
				Untyped: in.MetadataOptions.Forwarding.Untyped,
			}
		}
	}
	return &envoymatchingv3.ExtensionWithMatcher{
		ExtensionConfig: &envoycorev3.TypedExtensionConfig{
			Name:        "composite_ext_proc",
			TypedConfig: utils.MustMessageToAny(&envoycompositev3.Composite{}),
		},
		XdsMatcher: &xdsmatcherv3.Matcher{
			MatcherType: &xdsmatcherv3.Matcher_MatcherList_{
				MatcherList: &xdsmatcherv3.Matcher_MatcherList{
					Matchers: []*xdsmatcherv3.Matcher_MatcherList_FieldMatcher{
						{
							Predicate: &xdsmatcherv3.Matcher_MatcherList_Predicate{
								MatchType: &xdsmatcherv3.Matcher_MatcherList_Predicate_SinglePredicate_{
									SinglePredicate: &xdsmatcherv3.Matcher_MatcherList_Predicate_SinglePredicate{
										Input: &xdscorev3.TypedExtensionConfig{
											Name: globalFilterDisableMetadataKey,
											TypedConfig: utils.MustMessageToAny(&envoynetworkv3.DynamicMetadataInput{
												Filter: extProcGlobalDisableFilterMetadataNamespace,
												Path: []*envoynetworkv3.DynamicMetadataInput_PathSegment{
													{
														Segment: &envoynetworkv3.DynamicMetadataInput_PathSegment_Key{
															Key: globalFilterDisableMetadataKey,
														},
													},
												},
											}),
										},
										// This matcher succeeds when disable=true is not found in the dynamic metadata
										// for the extProcGlobalDisableFilterMetadataNamespace
										Matcher: &xdsmatcherv3.Matcher_MatcherList_Predicate_SinglePredicate_CustomMatch{
											CustomMatch: &xdscorev3.TypedExtensionConfig{
												Name: "envoy.matching.matchers.metadata_matcher",
												TypedConfig: utils.MustMessageToAny(&envoymetadatav3.Metadata{
													Value: &envoymatcherv3.ValueMatcher{
														MatchPattern: &envoymatcherv3.ValueMatcher_BoolMatch{
															BoolMatch: true,
														},
													},
													Invert: true,
												}),
											},
										},
									},
								},
							},
							OnMatch: &xdsmatcherv3.Matcher_OnMatch{
								OnMatch: &xdsmatcherv3.Matcher_OnMatch_Action{
									Action: &xdscorev3.TypedExtensionConfig{
										Name: "composite-action",
										TypedConfig: utils.MustMessageToAny(&envoycompositev3.ExecuteFilterAction{
											TypedConfig: &envoycorev3.TypedExtensionConfig{
												Name:        "envoy.filters.http.ext_proc",
												TypedConfig: utils.MustMessageToAny(filter),
											},
										}),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
