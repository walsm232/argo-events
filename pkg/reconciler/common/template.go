package common

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"github.com/argoproj/argo-events/pkg/apis/events/v1alpha1"
)

// MergeTemplate returns the template to use for an EventSource or Sensor
// deployment, with the given template applied on top of the defaults as a
// strategic merge patch. Values set in template take precedence, nested
// structs and maps are merged, and lists are merged by key where the type
// declares a patch merge key, otherwise replaced.
func MergeTemplate(defaults, template *v1alpha1.Template) (*v1alpha1.Template, error) {
	if defaults == nil {
		return template, nil
	}
	if template == nil {
		return defaults.DeepCopy(), nil
	}
	defaultsJSON, err := json.Marshal(defaults)
	if err != nil {
		return nil, err
	}
	templateJSON, err := json.Marshal(template)
	if err != nil {
		return nil, err
	}
	mergedJSON, err := strategicpatch.StrategicMergePatch(defaultsJSON, templateJSON, v1alpha1.Template{})
	if err != nil {
		return nil, err
	}
	merged := &v1alpha1.Template{}
	if err := json.Unmarshal(mergedJSON, merged); err != nil {
		return nil, err
	}
	return merged, nil
}
