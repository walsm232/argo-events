# Azure Functions

Azure Functions is a serverless compute service from Microsoft Azure. Argo Events makes it easy to
trigger Azure Functions from any event source supported by Argo Events.

<br/>
<br/>

## Trigger A Simple Azure Function

1.  Make sure to have eventbus deployed in the namespace.

1.  Create an Azure Function App and deploy a function (e.g. `hello`) to it. You can use the Azure portal, Azure CLI, etc.

1.  If your function uses function-level auth (the default), retrieve the function key and base64 encode it.

1.  Create a secret called `azure-function-secret` as follows.

        apiVersion: v1
        kind: Secret
        metadata:
          name: azure-function-secret
        type: Opaque
        data:
          functionKey: <base64-function-key>

1.  Let's set up a webhook event-source to invoke the Azure Function over HTTP requests.

        kubectl apply -n argo-events -f https://raw.githubusercontent.com/argoproj/argo-events/stable/examples/event-sources/webhook.yaml

1.  Let's expose the webhook event-source using `port-forward` so that we can make a request to it.

        kubectl -n argo-events port-forward <name-of-event-source-pod> 12000:12000

1.  Deploy the webhook sensor with Azure Functions trigger.

        kubectl apply -n argo-events -f https://raw.githubusercontent.com/argoproj/argo-events/stable/examples/sensors/azure-functions-trigger.yaml

1.  Once the sensor pod is in running state, make a `curl` request to webhook event-source pod,

         curl -d '{"name":"foo"}' -H "Content-Type: application/json" -X POST http://localhost:12000/example

1.  It will trigger the Azure Function `hello`. Check the Azure portal or Application Insights to verify the invocation.

## Specification

The Azure Functions trigger specification is available [here](../../APIs.md#argoproj.io/v1alpha1.AzureFunctionsTrigger).

## Request Payload

Invoking the Azure Function without a request payload would not be very useful. The Azure Functions trigger within a sensor
is invoked when the sensor receives an event from the eventbus. In order to construct a request payload based on the event data, sensor offers
`payload` field as a part of the Azure Functions trigger.

Let's examine an Azure Functions trigger,

                azureFunctions:
                  functionName: hello
                  appName: my-function-app
                  functionKey:
                    name: azure-function-secret
                    key: functionKey
                  payload:
                    - src:
                        dependencyName: test-dep
                        dataKey: body.name
                      dest: name

The `payload` contains the list of `src` which refers to the source event and `dest` which refers to destination key within result request payload.

The `payload` declared above will generate a request payload like below,

        {
          "name": "foo" // name field from event data
        }

The above payload will be sent as the HTTP body to invoke the Azure Function. You can add however many number of `src` and `dest` under `payload`.

**Note**: Take a look at [Parameterization](https://argoproj.github.io/argo-events/tutorials/02-parameterization/) in order to understand how to extract particular key-value from event data.

## Parameterization

Similar to other type of triggers, sensor offers parameterization for the Azure Functions trigger. Parameterization is specially useful when
you want to define a generic trigger template in the sensor and populate values like function name, app name, payload values on the fly.

Consider a scenario where you don't want to hard-code the function name and let the event data populate it.

                azureFunctions:
                  functionName: hello // this will be replaced.
                  appName: my-function-app
                  functionKey:
                    name: azure-function-secret
                    key: functionKey
                  payload:
                    - src:
                        dependencyName: test-dep
                        dataKey: body.message
                      dest: message
                  parameters:
                    - src:
                        dependencyName: test-dep
                        dataKey: body.function_name
                      dest: functionName

With `parameters` the sensor will replace the function name `hello` with the value of field `function_name` from event data.

You can learn more about trigger parameterization [here](https://argoproj.github.io/argo-events/tutorials/02-parameterization/).

## Policy

Trigger policy helps you determine the status of the Azure Function invocation and decide whether to stop or continue sensor.

To determine whether the function invocation was successful or not, the Azure Functions trigger provides a `Status` policy.
The `Status` holds a list of HTTP response status codes that are considered valid.

                        azureFunctions:
                          functionName: hello
                          appName: my-function-app
                          functionKey:
                            name: azure-function-secret
                            key: functionKey
                          payload:
                            - src:
                                dependencyName: test-dep
                                dataKey: body.message
                              dest: message
                          policy:
                            status:
                                allow:
                                    - 200
                                    - 202

The above Azure Functions trigger will be treated successful only if its invocation returns with either 200 or 202 status.

## Authentication

Azure Functions supports multiple levels of authorization:

- **Anonymous**: No key required. Set your function's authorization level to `anonymous` in Azure, and omit the `functionKey` field.
- **Function**: Requires a function-specific key. Store the key in a Kubernetes secret and reference it via `functionKey`.
- **Admin**: Requires the host (master) key. Store the master key in a Kubernetes secret and reference it via `functionKey`.

The key is sent via the `x-functions-key` HTTP header.
