package reconciler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
)

const testConfig = `
eventBus:
  jetstream:
    versions:
    - version: latest
      natsImage: nats:2.14.4
      metricsExporterImage: natsio/prometheus-nats-exporter:0.20.1
      configReloaderImage: natsio/nats-server-config-reloader:0.23.0
      startCommand: /nats-server
eventSource:
  template:
    securityContext:
      runAsNonRoot: true
    container:
      resources:
        requests:
          cpu: 100m
          memory: 64Mi
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "controller-config.yaml")
	assert.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestParseConfig(t *testing.T) {
	config, err := parseConfig(writeConfig(t, testConfig))
	assert.NoError(t, err)

	v, err := config.GetJetStreamVersion("latest")
	assert.NoError(t, err)
	assert.Equal(t, "nats:2.14.4", v.NatsImage)

	defaults := config.EventSourceTemplateDefaults()
	assert.True(t, *defaults.SecurityContext.RunAsNonRoot)
	assert.Equal(t, resource.MustParse("100m"), defaults.Container.Resources.Requests["cpu"])
	assert.Nil(t, config.SensorTemplateDefaults())
}

func TestConfigUpdate(t *testing.T) {
	config, err := parseConfig(writeConfig(t, testConfig))
	assert.NoError(t, err)

	same, err := parseConfig(writeConfig(t, testConfig))
	assert.NoError(t, err)
	eventSourceChanged, sensorChanged := config.update(same)
	assert.False(t, eventSourceChanged)
	assert.False(t, sensorChanged)

	changed, err := parseConfig(writeConfig(t, testConfig+`
sensor:
  template:
    serviceAccountName: sensor-sa
`))
	assert.NoError(t, err)
	eventSourceChanged, sensorChanged = config.update(changed)
	assert.False(t, eventSourceChanged)
	assert.True(t, sensorChanged)
	assert.Equal(t, "sensor-sa", config.SensorTemplateDefaults().ServiceAccountName)
}
