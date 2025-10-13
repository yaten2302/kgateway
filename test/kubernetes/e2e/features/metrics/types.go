//go:build e2e

package metrics

import (
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/fsutils"
	e2edefaults "github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/defaults"
	"github.com/kgateway-dev/kgateway/v2/test/kubernetes/e2e/tests/base"
)

var (
	// manifests
	setupManifest = filepath.Join(fsutils.MustGetThisDir(), "testdata", "setup.yaml")

	// objects
	proxyObjectMeta = metav1.ObjectMeta{
		Name:      "gw1",
		Namespace: "default",
	}

	kgatewayMetricsObjectMeta = metav1.ObjectMeta{
		Name:      "kgateway-metrics",
		Namespace: "kgateway-test",
	}

	setup = base.TestCase{
		Manifests: []string{setupManifest, e2edefaults.CurlPodManifest},
	}

	testCases = map[string]*base.TestCase{
		"TestMetrics": {},
	}
)
