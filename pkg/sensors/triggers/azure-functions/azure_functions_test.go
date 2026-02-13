/*
Copyright 2020 The Argoproj Authors.

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
package azure_functions

import (
	"context"
	"net/http"
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj/argo-events/pkg/apis/events/v1alpha1"
	"github.com/argoproj/argo-events/pkg/shared/logging"
)

var sensorObjSparse = &v1alpha1.Sensor{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "fake-sensor",
		Namespace: "fake",
	},
	Spec: v1alpha1.SensorSpec{
		Triggers: []v1alpha1.Trigger{
			{
				Template: &v1alpha1.TriggerTemplate{
					Name: "fake-trigger-sparse",
					AzureFunctions: &v1alpha1.AzureFunctionsTrigger{
						FunctionName: "fake-function",
						AppName:      "fake-app",
					},
				},
			},
		},
	},
}

var sensorObjFull = &v1alpha1.Sensor{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "fake-sensor",
		Namespace: "fake",
	},
	Spec: v1alpha1.SensorSpec{
		Triggers: []v1alpha1.Trigger{
			{
				Template: &v1alpha1.TriggerTemplate{
					Name: "fake-trigger-full",
					AzureFunctions: &v1alpha1.AzureFunctionsTrigger{
						FunctionName: "fake-function",
						AppName:      "fake-app",
						Payload: []v1alpha1.TriggerParameter{
							{
								Src: &v1alpha1.TriggerParameterSource{
									DependencyName: "fake-dependency",
									Value:          new(string),
								},
								Dest: "message",
							},
						},
					},
				},
			},
		},
	},
}

func getAzureFunctionsTriggers() []AzureFunctionsTrigger {
	return []AzureFunctionsTrigger{
		{
			HTTPClient: &http.Client{},
			Sensor:     sensorObjSparse.DeepCopy(),
			Trigger:    &sensorObjSparse.Spec.Triggers[0],
			Logger:     logging.NewArgoEventsLogger(),
		},
		{
			HTTPClient: &http.Client{},
			Sensor:     sensorObjFull.DeepCopy(),
			Trigger:    &sensorObjFull.Spec.Triggers[0],
			Logger:     logging.NewArgoEventsLogger(),
		},
	}
}

func TestAzureFunctionsTrigger_FetchResource(t *testing.T) {
	triggers := getAzureFunctionsTriggers()
	for _, trigger := range triggers {
		resource, err := trigger.FetchResource(context.TODO())
		assert.Nil(t, err)
		assert.NotNil(t, resource)

		at, ok := resource.(*v1alpha1.AzureFunctionsTrigger)
		assert.Nil(t, err)
		assert.Equal(t, true, ok)
		assert.Equal(t, "fake-function", at.FunctionName)
		assert.Equal(t, "fake-app", at.AppName)
	}
}

func TestAzureFunctionsTrigger_ApplyResourceParameters(t *testing.T) {
	triggers := getAzureFunctionsTriggers()
	for _, trigger := range triggers {
		testEvents := map[string]*v1alpha1.Event{
			"fake-dependency": {
				Context: &v1alpha1.EventContext{
					ID:              "1",
					Type:            "webhook",
					Source:          "webhook-gateway",
					DataContentType: "application/json",
					SpecVersion:     cloudevents.VersionV1,
					Subject:         "example-1",
				},
				Data: []byte(`{"function": "real-function", "app": "real-app"}`),
			},
		}

		defaultValue := "default"
		defaultApp := "default-app"

		trigger.Trigger.Template.AzureFunctions.Parameters = []v1alpha1.TriggerParameter{
			{
				Src: &v1alpha1.TriggerParameterSource{
					DependencyName: "fake-dependency",
					DataKey:        "function",
					Value:          &defaultValue,
				},
				Dest: "functionName",
			},
			{
				Src: &v1alpha1.TriggerParameterSource{
					DependencyName: "fake-dependency",
					DataKey:        "app",
					Value:          &defaultApp,
				},
				Dest: "appName",
			},
		}

		response, err := trigger.ApplyResourceParameters(testEvents, trigger.Trigger.Template.AzureFunctions)
		assert.Nil(t, err)
		assert.NotNil(t, response)

		updatedObj, ok := response.(*v1alpha1.AzureFunctionsTrigger)
		assert.Equal(t, true, ok)
		assert.Equal(t, "real-function", updatedObj.FunctionName)
		assert.Equal(t, "real-app", updatedObj.AppName)
	}
}

func TestAzureFunctionsTrigger_ApplyPolicy(t *testing.T) {
	triggers := getAzureFunctionsTriggers()
	for _, trigger := range triggers {
		response := &http.Response{
			StatusCode: 200,
		}
		trigger.Trigger.Policy = &v1alpha1.TriggerPolicy{
			Status: &v1alpha1.StatusPolicy{Allow: []int32{200, 201}},
		}
		err := trigger.ApplyPolicy(context.TODO(), response)
		assert.Nil(t, err)
	}
}
