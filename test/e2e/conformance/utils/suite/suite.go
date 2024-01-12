/*
Copyright 2022 The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package suite

import (
	"fmt"
	"testing"

	"github.com/alibaba/higress/test/e2e/conformance/utils/config"
	"github.com/alibaba/higress/test/e2e/conformance/utils/kubernetes"
	"github.com/alibaba/higress/test/e2e/conformance/utils/roundtripper"
	"istio.io/istio/pilot/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConformanceTestSuite defines the test suite used to run Gateway API
// conformance tests.
type ConformanceTestSuite struct {
	Client            client.Client
	RoundTripper      roundtripper.RoundTripper
	GatewayAddress    string
	IngressClassName  string
	Debug             bool
	Cleanup           bool
	BaseManifests     []string
	Applier           kubernetes.Applier
	SkipTests         sets.Set
	TimeoutConfig     config.TimeoutConfig
	SupportedFeatures sets.Set
}

// Options can be used to initialize a ConformanceTestSuite.
type Options struct {
	SupportedFeatures sets.Set
	ExemptFeatures    sets.Set

	EnableAllSupportedFeatures bool
	Client                     client.Client
	GatewayAddress             string
	IngressClassName           string
	Debug                      bool
	RoundTripper               roundtripper.RoundTripper
	BaseManifests              []string
	NamespaceLabels            map[string]string
	// Options for wasm extended features
	WASMOptions

	// CleanupBaseResources indicates whether or not the base test
	// resources such as Gateways should be cleaned up after the run.
	CleanupBaseResources bool
	TimeoutConfig        config.TimeoutConfig

	// IsEnvoyConfigTest indicates whether or not the test is for envoy config
	IsEnvoyConfigTest bool
}

type WASMOptions struct {
	IsWasmPluginTest bool
	WasmPluginType   string
	WasmPluginName   string
}

// New returns a new ConformanceTestSuite.
func New(s Options) *ConformanceTestSuite {
	config.SetupTimeoutConfig(&s.TimeoutConfig)

	roundTripper := s.RoundTripper
	if roundTripper == nil {
		roundTripper = &roundtripper.DefaultRoundTripper{Debug: s.Debug, TimeoutConfig: s.TimeoutConfig}
	}

	if s.SupportedFeatures == nil {
		s.SupportedFeatures = sets.Set{}
	}

	if s.IsWasmPluginTest {
		if s.WasmPluginType == "CPP" {
			s.SupportedFeatures.Insert(string(WASMCPPConformanceFeature))
		} else {
			s.SupportedFeatures.Insert(string(WASMGoConformanceFeature))
		}
	} else if s.IsEnvoyConfigTest {
		s.SupportedFeatures.Insert(string(EnvoyConfigConformanceFeature))
	} else if s.EnableAllSupportedFeatures {
		s.SupportedFeatures = AllFeatures
	}

	for feature := range s.ExemptFeatures {
		s.SupportedFeatures.Delete(feature)
	}

	suite := &ConformanceTestSuite{
		Client:            s.Client,
		RoundTripper:      roundTripper,
		IngressClassName:  s.IngressClassName,
		Debug:             s.Debug,
		Cleanup:           s.CleanupBaseResources,
		BaseManifests:     s.BaseManifests,
		SupportedFeatures: s.SupportedFeatures,
		GatewayAddress:    s.GatewayAddress,
		Applier: kubernetes.Applier{
			NamespaceLabels: s.NamespaceLabels,
		},
		TimeoutConfig: s.TimeoutConfig,
	}

	// apply defaults
	if suite.BaseManifests == nil {
		suite.BaseManifests = []string{
			"base/manifests.yaml",
			"base/consul.yaml",
			"base/eureka.yaml",
			"base/nacos.yaml",
			"base/dubbo.yaml",
		}
	}

	suite.Applier.IngressClass = suite.IngressClassName

	return suite
}

// Setup ensures the base resources required for conformance tests are installed
// in the cluster. It also ensures that all relevant resources are ready.
func (suite *ConformanceTestSuite) Setup(t *testing.T) {
	t.Logf("📦 Test Setup: Applying base manifests")

	for _, baseManifest := range suite.BaseManifests {
		suite.Applier.MustApplyWithCleanup(t, suite.Client, suite.TimeoutConfig, baseManifest, suite.Cleanup)
	}

	t.Logf("📦 Test Setup: Applying programmatic resources")
	secret := kubernetes.MustCreateSelfSignedCertSecret(t, "higress-conformance-web-backend", "certificate", []string{"*"})
	suite.Applier.MustApplyObjectsWithCleanup(t, suite.Client, suite.TimeoutConfig, []client.Object{secret}, suite.Cleanup)
	secret = kubernetes.MustCreateSelfSignedCertSecret(t, "higress-conformance-infra", "tls-validity-checks-certificate", []string{"*"})
	suite.Applier.MustApplyObjectsWithCleanup(t, suite.Client, suite.TimeoutConfig, []client.Object{secret}, suite.Cleanup)

	t.Logf("📦 Test Setup: Ensuring Pods from base manifests are ready")
	namespaces := []string{
		"higress-conformance-infra",
		"higress-conformance-app-backend",
		"higress-conformance-web-backend",
	}
	kubernetes.NamespacesMustBeAccepted(t, suite.Client, suite.TimeoutConfig, namespaces)

	t.Logf("🌱 Supported Features: %+v", suite.SupportedFeatures.UnsortedList())
}

// Run runs the provided set of conformance tests.
func (suite *ConformanceTestSuite) Run(t *testing.T, tests []ConformanceTest) {
	t.Logf("🚀 Start Running %d Test Cases: \n\n%s", len(tests), globalConformanceTestsListInfo(tests))
	for _, test := range tests {
		t.Run(test.ShortName, func(t *testing.T) {
			test.Run(t, suite)
		})
	}
}

// Clean ensures that all resources created by the test suite are cleaned up.
func (suite *ConformanceTestSuite) Clean(t *testing.T) {
	t.Logf("🧹 Test Cleanup: Ensuring all resources are cleaned up")
	suite.Applier.MustDelete(t, suite.Client, suite.TimeoutConfig, "base/manifests.yaml")
}

func globalConformanceTestsListInfo(tests []ConformanceTest) string {
	var cases string
	for index, test := range tests {
		cases += fmt.Sprintf("🎯 CaseNum: %d\nCaseName: %s\nScenario: %s\nFeatures: %+v\n\n", index+1, test.ShortName, test.Description, test.Features)
	}

	return cases
}

type ConformanceTests []ConformanceTest

// ConformanceTest is used to define each individual conformance test.
type ConformanceTest struct {
	ShortName   string
	Description string
	PreDeleteRs []string
	Manifests   []string
	Features    []SupportedFeature
	Slow        bool
	Parallel    bool
	Test        func(*testing.T, *ConformanceTestSuite)
	NotCleanup  bool
}

// Run runs an individual tests, applying and cleaning up the required manifests
// before calling the Test function.
func (test *ConformanceTest) Run(t *testing.T, suite *ConformanceTestSuite) {
	if test.Parallel {
		t.Parallel()
	}

	// Check that all features exercised by the test have been opted into by
	// the suite.
	for _, feature := range test.Features {
		if !suite.SupportedFeatures.Contains(string(feature)) {
			t.Skipf("🏊🏼 Skipping %s: suite does not support %s", test.ShortName, feature)
		}
	}

	// check that the test should not be skipped
	if suite.SkipTests.Contains(test.ShortName) {
		t.Skipf("🏊🏼 Skipping %s: test explicitly skipped", test.ShortName)
	}

	t.Logf("🔥 Running Conformance Test: %s", test.ShortName)

	for _, manifestLocation := range test.PreDeleteRs {
		t.Logf("🧳 Applying PreDeleteRs Manifests: %s", manifestLocation)
		suite.Applier.MustDelete(t, suite.Client, suite.TimeoutConfig, manifestLocation)
	}

	for _, manifestLocation := range test.Manifests {
		t.Logf("🧳 Applying Manifests: %s", manifestLocation)
		suite.Applier.MustApplyWithCleanup(t, suite.Client, suite.TimeoutConfig, manifestLocation, !test.NotCleanup)
	}

	test.Test(t, suite)
}
