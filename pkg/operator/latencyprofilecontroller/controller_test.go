package latencyprofilecontroller

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	controlplanev1 "github.com/openshift/api/kubecontrolplane/v1"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

func TestConfigMatchesControllerManagerArguments(t *testing.T) {
	createConfigMapFromCMConfig := func(
		config controlplanev1.KubeControllerManagerConfig,
		configMapName, configMapNamespace string,
	) (configMap corev1.ConfigMap) {

		configAsJsonBytes, err := json.MarshalIndent(config, "", "")
		require.NoError(t, err)

		configMap = corev1.ConfigMap{
			ObjectMeta: v1.ObjectMeta{Name: configMapName, Namespace: configMapNamespace},
			Data: map[string]string{
				controllerManagerConfigMapKey: string(configAsJsonBytes),
			},
		}
		return configMap
	}

	cmConfigs := []controlplanev1.KubeControllerManagerConfig{
		// config 1
		{},
		// config 2
		{
			ExtendedArguments: map[string]controlplanev1.Arguments{
				"default-node-monitor-grace-period": []string{"40s"},
				"node-monitor-period":               []string{"5s"},
			},
		},
		// config 3
		{
			ExtendedArguments: map[string]controlplanev1.Arguments{
				"node-monitor-period": []string{"5s"},
			},
		},
		// config 4
		{
			ExtendedArguments: map[string]controlplanev1.Arguments{
				"default-node-monitor-grace-period": []string{"2m"},
				"bind-address":                      []string{"0.0.0.0"},
			},
		},
	}
	cmConfigMaps := make([]corev1.ConfigMap, len(cmConfigs))
	for i, cmConfig := range cmConfigs {
		cmConfigMaps[i] = createConfigMapFromCMConfig(
			cmConfig, fmt.Sprintf("%s-%d", controllerManagerConfigMapName, i),
			operatorclient.TargetNamespace)
	}

	scenarios := []struct {
		name                       string
		controllerManagerConfig    *controlplanev1.KubeControllerManagerConfig
		controllerManagerConfigMap *corev1.ConfigMap
		argVals                    map[string]string
		expectedMatch              bool
	}{
		{
			name: "arg=bind-address should not be found in config with empty extendedArgs",

			// config with empty extendedArgs
			controllerManagerConfig:    &cmConfigs[0],
			controllerManagerConfigMap: &cmConfigMaps[0],

			argVals:       map[string]string{"bind-address": "0.0.0.0"},
			expectedMatch: false,
		},
		{
			name: "arg=default-node-monitor-grace-period with value=40s should be found in config",

			// config with extendedArgs{default-node-monitor-grace-period=40s,node-monitor-period}
			controllerManagerConfig:    &cmConfigs[1],
			controllerManagerConfigMap: &cmConfigMaps[1],

			argVals:       map[string]string{"default-node-monitor-grace-period": "40s"},
			expectedMatch: true,
		},
		{
			name: "arg=default-node-monitor-grace-period should not be found in config which does not contain that arg",

			// config with extendedArgs{node-monitor-period}
			controllerManagerConfig:    &cmConfigs[2],
			controllerManagerConfigMap: &cmConfigMaps[2],

			argVals:       map[string]string{"default-node-monitor-grace-period": "2m"},
			expectedMatch: false,
		},
		{
			name: "arg=default-node-monitor-grace-period with value=40s should not be found in config which contains that arg but different value",

			// config with extendedArgs{default-node-monitor-grace-period=2m,bind-address}
			controllerManagerConfig:    &cmConfigs[3],
			controllerManagerConfigMap: &cmConfigMaps[3],

			argVals:       map[string]string{"default-node-monitor-grace-period": "40s"},
			expectedMatch: false,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// act
			actualMatch, err := configMatchesControllerManagerArguments(scenario.controllerManagerConfigMap, scenario.argVals)
			if err != nil {
				// in case error is encountered during matching
				t.Fatal(err)
			}
			// validate
			if !(actualMatch == scenario.expectedMatch) {
				containStr := "should contain"
				if !scenario.expectedMatch {
					containStr = "should not contain"
				}
				t.Fatalf("unexpected matching, expected = %v but got %v; controller-manager-config=%v %s %v",
					scenario.expectedMatch, actualMatch,
					*scenario.controllerManagerConfig, containStr,
					scenario.argVals)
			}
		})
	}
}
