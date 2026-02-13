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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/argoproj/argo-events/pkg/apis/events/v1alpha1"
	"github.com/argoproj/argo-events/pkg/sensors/policy"
	"github.com/argoproj/argo-events/pkg/sensors/triggers"
	"github.com/argoproj/argo-events/pkg/shared/logging"
	sharedutil "github.com/argoproj/argo-events/pkg/shared/util"
)

// AzureFunctionsTrigger refers to trigger that invokes Azure Functions
type AzureFunctionsTrigger struct {
	// HTTPClient is the HTTP client used to invoke the function
	HTTPClient *http.Client
	// Sensor object
	Sensor *v1alpha1.Sensor
	// Trigger definition
	Trigger *v1alpha1.Trigger
	// Logger to log stuff
	Logger *zap.SugaredLogger
}

// NewAzureFunctionsTrigger returns a new Azure Functions trigger context
func NewAzureFunctionsTrigger(httpClients sharedutil.StringKeyedMap[*http.Client], sensor *v1alpha1.Sensor, trigger *v1alpha1.Trigger, logger *zap.SugaredLogger) (*AzureFunctionsTrigger, error) {
	httpClient, ok := httpClients.Load(trigger.Template.Name)
	if !ok {
		httpClient = &http.Client{
			Timeout: time.Minute * 1,
		}
		httpClients.Store(trigger.Template.Name, httpClient)
	}

	return &AzureFunctionsTrigger{
		HTTPClient: httpClient,
		Sensor:     sensor,
		Trigger:    trigger,
		Logger:     logger.With(logging.LabelTriggerType, v1alpha1.TriggerTypeAzureFunctions),
	}, nil
}

// GetTriggerType returns the type of the trigger
func (t *AzureFunctionsTrigger) GetTriggerType() v1alpha1.TriggerType {
	return v1alpha1.TriggerTypeAzureFunctions
}

// FetchResource fetches the trigger resource
func (t *AzureFunctionsTrigger) FetchResource(ctx context.Context) (interface{}, error) {
	return t.Trigger.Template.AzureFunctions, nil
}

// ApplyResourceParameters applies parameters to the trigger resource
func (t *AzureFunctionsTrigger) ApplyResourceParameters(events map[string]*v1alpha1.Event, resource interface{}) (interface{}, error) {
	resourceBytes, err := json.Marshal(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal the azure functions trigger resource, %w", err)
	}
	parameters := t.Trigger.Template.AzureFunctions.Parameters
	if parameters != nil {
		updatedResourceBytes, err := triggers.ApplyParams(resourceBytes, t.Trigger.Template.AzureFunctions.Parameters, events)
		if err != nil {
			return nil, err
		}
		var ht *v1alpha1.AzureFunctionsTrigger
		if err := json.Unmarshal(updatedResourceBytes, &ht); err != nil {
			return nil, fmt.Errorf("failed to unmarshal the updated azure functions trigger resource after applying resource parameters, %w", err)
		}
		return ht, nil
	}
	return resource, nil
}

// Execute executes the trigger
func (t *AzureFunctionsTrigger) Execute(ctx context.Context, events map[string]*v1alpha1.Event, resource interface{}) (interface{}, error) {
	trigger, ok := resource.(*v1alpha1.AzureFunctionsTrigger)
	if !ok {
		return nil, fmt.Errorf("failed to interpret the trigger resource")
	}

	if trigger.Payload == nil {
		return nil, fmt.Errorf("payload parameters are not specified")
	}

	payload, err := triggers.ConstructPayload(events, trigger.Payload)
	if err != nil {
		return nil, err
	}

	// Construct the Azure Functions URL: https://<appName>.azurewebsites.net/api/<functionName>
	functionURL := fmt.Sprintf("https://%s.azurewebsites.net/api/%s", trigger.AppName, trigger.FunctionName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, functionURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request for Azure Function, %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// If a function key is provided, set the x-functions-key header for authentication
	if trigger.FunctionKey != nil {
		functionKey, err := sharedutil.GetSecretFromVolume(trigger.FunctionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve the Azure Function key, %w", err)
		}
		req.Header.Set("x-functions-key", functionKey)
	}

	t.Logger.Infow("invoking Azure Function", "appName", trigger.AppName, "functionName", trigger.FunctionName)

	response, err := t.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke Azure Function, %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body from Azure Function, %w", err)
	}

	t.Logger.Infow("Azure Function invoked successfully",
		"appName", trigger.AppName,
		"functionName", trigger.FunctionName,
		"statusCode", response.StatusCode,
	)

	return &http.Response{
		StatusCode: response.StatusCode,
		Body:       io.NopCloser(bytes.NewBuffer(body)),
	}, nil
}

// ApplyPolicy applies the policy on the trigger execution response
func (t *AzureFunctionsTrigger) ApplyPolicy(ctx context.Context, resource interface{}) error {
	if t.Trigger.Policy == nil || t.Trigger.Policy.Status == nil || t.Trigger.Policy.Status.Allow == nil {
		return nil
	}

	obj, ok := resource.(*http.Response)
	if !ok {
		return fmt.Errorf("failed to interpret the trigger resource")
	}

	p := policy.NewStatusPolicy(obj.StatusCode, t.Trigger.Policy.Status.GetAllow())
	return p.ApplyPolicy(ctx)
}
