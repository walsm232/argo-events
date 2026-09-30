package reconciler

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/yaml"

	aev1 "github.com/argoproj/argo-events/pkg/apis/events/v1alpha1"
	sharedutil "github.com/argoproj/argo-events/pkg/shared/util"
)

type GlobalConfig struct {
	EventBus *EventBusConfig `json:"eventBus"`
	// EventSource holds the defaults applied to every EventSource
	EventSource *TemplateDefaults `json:"eventSource,omitempty"`
	// Sensor holds the defaults applied to every Sensor
	Sensor *TemplateDefaults `json:"sensor,omitempty"`

	mu sync.RWMutex
}

// TemplateDefaults holds a template that is merged into the spec.template of
// each EventSource or Sensor. Values set on the object take precedence.
type TemplateDefaults struct {
	Template *aev1.Template `json:"template,omitempty"`
}

type EventBusConfig struct {
	NATS      *StanConfig      `json:"nats"`
	JetStream *JetStreamConfig `json:"jetstream"`
}

type StanConfig struct {
	Versions []StanVersion `json:"versions"`
}

type StanVersion struct {
	Version              string `json:"version"`
	NATSStreamingImage   string `json:"natsStreamingImage"`
	MetricsExporterImage string `json:"metricsExporterImage"`
}

type JetStreamConfig struct {
	Settings     string             `json:"settings"`
	StreamConfig string             `json:"streamConfig"`
	Versions     []JetStreamVersion `json:"versions"`
}

type JetStreamVersion struct {
	Version              string `json:"version"`
	NatsImage            string `json:"natsImage"`
	ConfigReloaderImage  string `json:"configReloaderImage"`
	MetricsExporterImage string `json:"metricsExporterImage"`
	StartCommand         string `json:"startCommand"`
}

func (g *GlobalConfig) supportedSTANVersions() []string {
	result := []string{}
	if g.EventBus == nil || g.EventBus.NATS == nil {
		return result
	}
	for _, v := range g.EventBus.NATS.Versions {
		result = append(result, v.Version)
	}
	return result
}

func (g *GlobalConfig) supportedJetStreamVersions() []string {
	result := []string{}
	if g.EventBus == nil || g.EventBus.JetStream == nil {
		return result
	}
	for _, v := range g.EventBus.JetStream.Versions {
		result = append(result, v.Version)
	}
	return result
}

func (g *GlobalConfig) GetSTANVersion(version string) (*StanVersion, error) {
	if g.EventBus == nil || g.EventBus.NATS == nil {
		return nil, fmt.Errorf("\"eventBus.nats\" not found in the configuration")
	}
	if len(g.EventBus.NATS.Versions) == 0 {
		return nil, fmt.Errorf("nats streaming version configuration not found")
	}
	for _, r := range g.EventBus.NATS.Versions {
		if r.Version == version {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("unsupported version %q, supported versions: %q", version, strings.Join(g.supportedSTANVersions(), ","))
}

func (g *GlobalConfig) GetJetStreamVersion(version string) (*JetStreamVersion, error) {
	if g.EventBus == nil || g.EventBus.JetStream == nil {
		return nil, fmt.Errorf("\"eventBus.jetstream\" not found in the configuration")
	}
	if len(g.EventBus.JetStream.Versions) == 0 {
		return nil, fmt.Errorf("jetstream version configuration not found")
	}
	for _, r := range g.EventBus.JetStream.Versions {
		if r.Version == version {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("unsupported version %q, supported versions: %q", version, strings.Join(g.supportedJetStreamVersions(), ","))
}

// EventSourceTemplateDefaults returns a copy of the configured EventSource template defaults, or nil.
func (g *GlobalConfig) EventSourceTemplateDefaults() *aev1.Template {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.EventSource == nil {
		return nil
	}
	return g.EventSource.Template.DeepCopy()
}

// SensorTemplateDefaults returns a copy of the configured Sensor template defaults, or nil.
func (g *GlobalConfig) SensorTemplateDefaults() *aev1.Template {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.Sensor == nil {
		return nil
	}
	return g.Sensor.Template.DeepCopy()
}

// update replaces the configuration with a newly loaded one, and reports
// whether the EventSource and Sensor template defaults changed.
func (g *GlobalConfig) update(n *GlobalConfig) (eventSourceChanged, sensorChanged bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	eventSourceChanged = !equality.Semantic.DeepEqual(g.EventSource, n.EventSource)
	sensorChanged = !equality.Semantic.DeepEqual(g.Sensor, n.Sensor)
	g.EventBus = n.EventBus
	g.EventSource = n.EventSource
	g.Sensor = n.Sensor
	return eventSourceChanged, sensorChanged
}

// parseConfig reads the configuration file. It uses sigs.k8s.io/yaml rather
// than viper's decoder so that Kubernetes types such as resource quantities
// in the template defaults are decoded through their JSON tags.
func parseConfig(path string) (*GlobalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r := &GlobalConfig{}
	if err := yaml.Unmarshal(data, r); err != nil {
		return nil, err
	}
	return r, nil
}

// LoadConfig loads the configuration file and watches it for changes. On a
// reload that changes the EventSource or Sensor template defaults,
// onDefaultsChanged is called so the affected objects can be reconciled.
func LoadConfig(onErrorReloading func(error), onDefaultsChanged func(eventSourceChanged, sensorChanged bool)) (*GlobalConfig, error) {
	v := sharedutil.ViperWithLogging()
	v.SetConfigName("controller-config")
	v.SetConfigType("yaml")
	v.AddConfigPath("/etc/argo-events")
	err := v.ReadInConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration file. %w", err)
	}
	r, err := parseConfig(v.ConfigFileUsed())
	if err != nil {
		return nil, fmt.Errorf("failed unmarshal configuration file. %w", err)
	}
	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		n, err := parseConfig(v.ConfigFileUsed())
		if err != nil {
			onErrorReloading(err)
			return
		}
		eventSourceChanged, sensorChanged := r.update(n)
		if (eventSourceChanged || sensorChanged) && onDefaultsChanged != nil {
			onDefaultsChanged(eventSourceChanged, sensorChanged)
		}
	})
	return r, nil
}

func ValidateConfig(config *GlobalConfig) error {
	if len(config.supportedJetStreamVersions()) == 0 {
		return fmt.Errorf("no jetstream versions were provided in the controller config")
	}

	if len(config.supportedSTANVersions()) == 0 {
		return fmt.Errorf("no stan versions were provided in the controller config")
	}

	return nil
}
