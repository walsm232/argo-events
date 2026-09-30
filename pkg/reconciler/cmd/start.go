package cmd

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	argoevents "github.com/argoproj/argo-events"
	aev1 "github.com/argoproj/argo-events/pkg/apis/events/v1alpha1"
	"github.com/argoproj/argo-events/pkg/reconciler"
	"github.com/argoproj/argo-events/pkg/reconciler/eventbus"
	"github.com/argoproj/argo-events/pkg/reconciler/eventsource"
	"github.com/argoproj/argo-events/pkg/reconciler/sensor"
	"github.com/argoproj/argo-events/pkg/shared/logging"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	imageEnvVar = "ARGO_EVENTS_IMAGE"
)

type ArgoEventsControllerOpts struct {
	Namespaced       bool
	ManagedNamespace string
	LeaderElection   bool
	MetricsPort      int32
	HealthPort       int32
}

func Start(eventsOpts ArgoEventsControllerOpts) {
	logger := logging.NewArgoEventsLogger().Named(eventbus.ControllerName)
	// Signalled when the EventSource or Sensor template defaults change, so
	// that every object is reconciled and its deployment rolled out.
	eventSourceDefaultsChanged := make(chan event.GenericEvent, 1)
	sensorDefaultsChanged := make(chan event.GenericEvent, 1)
	config, err := reconciler.LoadConfig(func(err error) {
		logger.Errorw("Failed to reload global configuration file", zap.Error(err))
	}, func(eventSourceChanged, sensorChanged bool) {
		if eventSourceChanged {
			logger.Info("EventSource template defaults changed, reconciling all EventSources")
			notify(eventSourceDefaultsChanged, &aev1.EventSource{})
		}
		if sensorChanged {
			logger.Info("Sensor template defaults changed, reconciling all Sensors")
			notify(sensorDefaultsChanged, &aev1.Sensor{})
		}
	})
	if err != nil {
		logger.Fatalw("Failed to load global configuration file", zap.Error(err))
	}

	if err = reconciler.ValidateConfig(config); err != nil {
		logger.Fatalw("Global configuration file validation failed", zap.Error(err))
	}

	imageName, defined := os.LookupEnv(imageEnvVar)
	if !defined {
		logger.Fatalf("required environment variable '%s' not defined", imageEnvVar)
	}
	opts := ctrl.Options{
		Metrics: metricsserver.Options{
			BindAddress: fmt.Sprintf(":%d", eventsOpts.MetricsPort),
		},
		HealthProbeBindAddress: fmt.Sprintf(":%d", eventsOpts.HealthPort),
	}
	if eventsOpts.Namespaced {
		opts.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				eventsOpts.ManagedNamespace: {},
			},
		}
	}
	if eventsOpts.LeaderElection {
		opts.LeaderElection = true
		opts.LeaderElectionID = "argo-events-controller"
	}
	restConfig := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(restConfig, opts)
	if err != nil {
		logger.Fatalw("Unable to get a controller-runtime manager", zap.Error(err))
	}
	kubeClient := kubernetes.NewForConfigOrDie(restConfig)

	// Readyness probe
	if err := mgr.AddReadyzCheck("readiness", healthz.Ping); err != nil {
		logger.Fatalw("Unable add a readiness check", zap.Error(err))
	}

	// Liveness probe
	if err := mgr.AddHealthzCheck("liveness", healthz.Ping); err != nil {
		logger.Fatalw("Unable add a health check", zap.Error(err))
	}

	if err := aev1.AddToScheme(mgr.GetScheme()); err != nil {
		logger.Fatalw("Unable to add scheme", zap.Error(err))
	}

	// EventBus controller
	eventBusController, err := controller.New(eventbus.ControllerName, mgr, controller.Options{
		Reconciler: eventbus.NewReconciler(mgr.GetClient(), kubeClient, mgr.GetScheme(), config, logger),
	})
	if err != nil {
		logger.Fatalw("Unable to set up EventBus controller", zap.Error(err))
	}

	// Watch EventBus and enqueue EventBus object key
	if err := eventBusController.Watch(source.Kind(mgr.GetCache(), &aev1.EventBus{}, &handler.TypedEnqueueRequestForObject[*aev1.EventBus]{},
		predicate.Or(
			predicate.TypedGenerationChangedPredicate[*aev1.EventBus]{},
			predicate.TypedLabelChangedPredicate[*aev1.EventBus]{},
		))); err != nil {
		logger.Fatalw("Unable to watch EventBus", zap.Error(err))
	}

	// Watch ConfigMaps and enqueue owning EventBus key
	if err := eventBusController.Watch(source.Kind(mgr.GetCache(), &corev1.ConfigMap{},
		handler.TypedEnqueueRequestForOwner[*corev1.ConfigMap](mgr.GetScheme(), mgr.GetRESTMapper(), &aev1.EventBus{}, handler.OnlyControllerOwner()),
		predicate.TypedGenerationChangedPredicate[*corev1.ConfigMap]{})); err != nil {
		logger.Fatalw("Unable to watch ConfigMaps", zap.Error(err))
	}

	// Watch StatefulSets and enqueue owning EventBus key
	if err := eventBusController.Watch(source.Kind(mgr.GetCache(), &appv1.StatefulSet{},
		handler.TypedEnqueueRequestForOwner[*appv1.StatefulSet](mgr.GetScheme(), mgr.GetRESTMapper(), &aev1.EventBus{}, handler.OnlyControllerOwner()),
		predicate.TypedGenerationChangedPredicate[*appv1.StatefulSet]{})); err != nil {
		logger.Fatalw("Unable to watch StatefulSets", zap.Error(err))
	}

	// Watch Services and enqueue owning EventBus key
	if err := eventBusController.Watch(source.Kind(mgr.GetCache(), &corev1.Service{},
		handler.TypedEnqueueRequestForOwner[*corev1.Service](mgr.GetScheme(), mgr.GetRESTMapper(), &aev1.EventBus{}, handler.OnlyControllerOwner()),
		predicate.TypedGenerationChangedPredicate[*corev1.Service]{})); err != nil {
		logger.Fatalw("Unable to watch Services", zap.Error(err))
	}

	// EventSource controller
	eventSourceController, err := controller.New(eventsource.ControllerName, mgr, controller.Options{
		Reconciler: eventsource.NewReconciler(mgr.GetClient(), mgr.GetScheme(), imageName, config.EventSourceTemplateDefaults, logger),
	})
	if err != nil {
		logger.Fatalw("Unable to set up EventSource controller", zap.Error(err))
	}

	// Watch EventSource and enqueue EventSource object key
	if err := eventSourceController.Watch(source.Kind(mgr.GetCache(), &aev1.EventSource{}, &handler.TypedEnqueueRequestForObject[*aev1.EventSource]{},
		predicate.Or(
			predicate.TypedGenerationChangedPredicate[*aev1.EventSource]{},
			predicate.TypedLabelChangedPredicate[*aev1.EventSource]{},
		))); err != nil {
		logger.Fatalw("Unable to watch EventSources", zap.Error(err))
	}

	// Watch Deployments and enqueue owning EventSource key
	if err := eventSourceController.Watch(source.Kind(mgr.GetCache(), &appv1.Deployment{},
		handler.TypedEnqueueRequestForOwner[*appv1.Deployment](mgr.GetScheme(), mgr.GetRESTMapper(), &aev1.EventSource{}, handler.OnlyControllerOwner()),
		predicate.TypedGenerationChangedPredicate[*appv1.Deployment]{})); err != nil {
		logger.Fatalw("Unable to watch Deployments", zap.Error(err))
	}

	// Watch Services and enqueue owning EventSource key
	if err := eventSourceController.Watch(source.Kind(mgr.GetCache(), &corev1.Service{},
		handler.TypedEnqueueRequestForOwner[*corev1.Service](mgr.GetScheme(), mgr.GetRESTMapper(), &aev1.EventSource{}, handler.OnlyControllerOwner()),
		predicate.TypedGenerationChangedPredicate[*corev1.Service]{})); err != nil {
		logger.Fatalw("Unable to watch Services", zap.Error(err))
	}

	// Reconcile all EventSources when the template defaults change
	if err := eventSourceController.Watch(source.Channel(eventSourceDefaultsChanged,
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
			list := &aev1.EventSourceList{}
			if err := mgr.GetClient().List(ctx, list); err != nil {
				logger.Errorw("Failed to list EventSources", zap.Error(err))
				return nil
			}
			requests := make([]reconcile.Request, 0, len(list.Items))
			for _, es := range list.Items {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&es)})
			}
			return requests
		}))); err != nil {
		logger.Fatalw("Unable to watch EventSource template defaults", zap.Error(err))
	}

	// Sensor controller
	sensorController, err := controller.New(sensor.ControllerName, mgr, controller.Options{
		Reconciler: sensor.NewReconciler(mgr.GetClient(), mgr.GetScheme(), imageName, config.SensorTemplateDefaults, logger),
	})
	if err != nil {
		logger.Fatalw("Unable to set up Sensor controller", zap.Error(err))
	}

	// Watch Sensor and enqueue Sensor object key
	if err := sensorController.Watch(source.Kind(mgr.GetCache(), &aev1.Sensor{}, &handler.TypedEnqueueRequestForObject[*aev1.Sensor]{},
		predicate.Or(
			predicate.TypedGenerationChangedPredicate[*aev1.Sensor]{},
			predicate.TypedLabelChangedPredicate[*aev1.Sensor]{},
		))); err != nil {
		logger.Fatalw("Unable to watch Sensors", zap.Error(err))
	}

	// Watch Deployments and enqueue owning Sensor key
	if err := sensorController.Watch(source.Kind(mgr.GetCache(), &appv1.Deployment{},
		handler.TypedEnqueueRequestForOwner[*appv1.Deployment](mgr.GetScheme(), mgr.GetRESTMapper(), &aev1.Sensor{}, handler.OnlyControllerOwner()),
		predicate.TypedGenerationChangedPredicate[*appv1.Deployment]{})); err != nil {
		logger.Fatalw("Unable to watch Deployments", zap.Error(err))
	}

	// Reconcile all Sensors when the template defaults change
	if err := sensorController.Watch(source.Channel(sensorDefaultsChanged,
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
			list := &aev1.SensorList{}
			if err := mgr.GetClient().List(ctx, list); err != nil {
				logger.Errorw("Failed to list Sensors", zap.Error(err))
				return nil
			}
			requests := make([]reconcile.Request, 0, len(list.Items))
			for _, s := range list.Items {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&s)})
			}
			return requests
		}))); err != nil {
		logger.Fatalw("Unable to watch Sensor template defaults", zap.Error(err))
	}

	logger.Infow("Starting controller manager", "version", argoevents.GetVersion())
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		logger.Fatalw("Unable to start controller manager", zap.Error(err))
	}
}

// notify sends a non-blocking signal on ch. The channel has a buffer of one,
// so changes made before the previous signal was handled are coalesced.
func notify(ch chan<- event.GenericEvent, obj client.Object) {
	select {
	case ch <- event.GenericEvent{Object: obj}:
	default:
	}
}
