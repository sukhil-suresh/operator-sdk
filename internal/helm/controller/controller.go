// Copyright 2018 The Operator-SDK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/sergi/go-diff/diffmatchpatch"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"strings"
	"sync"
	"time"

	rpb "helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/releaseutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	crthandler "sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/yaml"

	libhandler "github.com/operator-framework/operator-lib/handler"
	"github.com/operator-framework/operator-lib/predicate"
	"github.com/operator-framework/operator-sdk/internal/helm/release"
	"github.com/operator-framework/operator-sdk/internal/util/k8sutil"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	predicate2 "sigs.k8s.io/controller-runtime/pkg/predicate"
)

var log = logf.Log.WithName("helm.controller")

// WatchOptions contains the necessary values to create a new controller that
// manages helm releases in a particular namespace based on a GVK watch.
type WatchOptions struct {
	GVK                     schema.GroupVersionKind
	ManagerFactory          release.ManagerFactory
	ReconcilePeriod         time.Duration
	WatchDependentResources bool
	OverrideValues          map[string]string
	SuppressOverrideValues  bool
	MaxConcurrentReconciles int
	Selector                metav1.LabelSelector
	DryRunOption            string
}

// Add creates a new helm operator controller and adds it to the manager
func Add(mgr manager.Manager, options WatchOptions) error {
	mgr.GetClient()
	controllerName := fmt.Sprintf("%v-controller", strings.ToLower(options.GVK.Kind))

	r := &HelmOperatorReconciler{
		Client:                 mgr.GetClient(),
		EventRecorder:          mgr.GetEventRecorderFor(controllerName),
		GVK:                    options.GVK,
		ManagerFactory:         options.ManagerFactory,
		ReconcilePeriod:        options.ReconcilePeriod,
		OverrideValues:         options.OverrideValues,
		SuppressOverrideValues: options.SuppressOverrideValues,
		DryRunOption:           options.DryRunOption,
	}

	c, err := controller.New(controllerName, mgr, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: options.MaxConcurrentReconciles,
	})
	if err != nil {
		return err
	}

	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(options.GVK)

	var customPredicates = predicate2.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			log.Info("~~~~~~~~~~~~~> Create event: Object %s/%s\n", e.Object.GetNamespace(), e.Object.GetName())
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			log.Info("~~~~~~~~~~~~~> Update event: Object %s/%s\n", e.ObjectNew.GetNamespace(), e.ObjectNew.GetName())
			uOld := e.ObjectOld.(*unstructured.Unstructured)
			uNew := e.ObjectNew.(*unstructured.Unstructured)

			fields := []string{"metadata", "spec", "status"}
			for _, f := range fields {
				oldF, _, _ := unstructured.NestedMap(uOld.Object, f)
				newF, _, _ := unstructured.NestedMap(uNew.Object, f)

				oldBytes, _ := json.Marshal(oldF)
				newBytes, _ := json.Marshal(newF)

				oldStr := string(oldBytes)
				newStr := string(newBytes)

				if oldStr != newStr {
					//dmp := diffmatchpatch.New()
					//diffs := dmp.DiffMain(oldStr, newStr, false)
					log.Info("--DIFF--" + f)
					log.Info(oldStr)
					log.Info(newStr)
				} else {
					log.Info("--NO-DIFF--" + f)
				}
			}
			log.Info("<~~~~~~~~~~~~~")

			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("Delete event: Object %s/%s\n", e.Object.GetNamespace(), e.Object.GetName())
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			log.Info("Generic event: Object %s/%s\n", e.Object.GetNamespace(), e.Object.GetName())
			return true
		},
	}

	if err := c.Watch(source.Kind(mgr.GetCache(), client.Object(o), &libhandler.InstrumentedEnqueueRequestForObject[client.Object]{}, customPredicates)); err != nil {
		return err
	}

	//if options.WatchDependentResources {
	//	watchDependentResources(mgr, r, c)
	//}

	log.Info("Watching resource", "apiVersion", options.GVK.GroupVersion(), "kind",
		options.GVK.Kind, "reconcilePeriod", options.ReconcilePeriod.String())
	return nil
}

var customPredicates = predicate2.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		fmt.Printf("~~~~~~~~~~~~~> Create event: Object %s/%s\n", e.Object.GetNamespace(), e.Object.GetName())
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		fmt.Printf("~~~~~~~~~~~~~> Update event: Object %s/%s\n", e.ObjectNew.GetNamespace(), e.ObjectNew.GetName())
		var data1 string
		var data2 string
		var err error
		if e.ObjectOld != nil {
			data1, err = PrettyPrint(e.ObjectOld)
			if err == nil {
				fmt.Println(data1)
			}
		}
		fmt.Println()
		if e.ObjectNew != nil {
			data2, err = PrettyPrint(e.ObjectNew)
			if err == nil {
				fmt.Println(data2)
			}
		}
		if data1 != "" && data2 != "" {
			fmt.Println()
			dmp := diffmatchpatch.New()
			diffs := dmp.DiffMain(data1, data2, false)
			fmt.Println(dmp.DiffPrettyText(diffs))
		}
		fmt.Printf("<~~~~~~~~~~~~~\n")
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		fmt.Printf("Delete event: Object %s/%s\n", e.Object.GetNamespace(), e.Object.GetName())
		return true
	},
	GenericFunc: func(e event.GenericEvent) bool {
		fmt.Printf("Generic event: Object %s/%s\n", e.Object.GetNamespace(), e.Object.GetName())
		return true
	},
}

func PrettyPrint(obj client.Object) (string, error) {
	// Create a new JSON serializer
	serializer := k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, nil, nil, false)

	// Marshal the object to JSON
	var buf bytes.Buffer
	err := serializer.Encode(obj, &buf)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// watchDependentResources adds a release hook function to the HelmOperatorReconciler
// that adds watches for resources in released Helm charts.
func watchDependentResources(mgr manager.Manager, r *HelmOperatorReconciler, c controller.Controller) {
	var m sync.RWMutex
	watches := map[schema.GroupVersionKind]struct{}{}
	releaseHook := func(release *rpb.Release) error {
		owner := &unstructured.Unstructured{}
		owner.SetGroupVersionKind(r.GVK)
		owner.SetNamespace(release.Namespace)
		resources := releaseutil.SplitManifests(release.Manifest)
		for _, resource := range resources {
			var u unstructured.Unstructured
			if err := yaml.Unmarshal([]byte(resource), &u); err != nil {
				return err
			}

			gvk := u.GroupVersionKind()
			if gvk.Empty() {
				continue
			}

			var setWatchOnResource = func(dependent runtime.Object) error {
				unstructuredObj := dependent.(*unstructured.Unstructured)
				gvkDependent := unstructuredObj.GroupVersionKind()
				if gvkDependent.Empty() {
					return nil
				}

				m.RLock()
				_, ok := watches[gvkDependent]
				m.RUnlock()
				if ok {
					return nil
				}

				restMapper := mgr.GetRESTMapper()
				useOwnerRef, err := k8sutil.SupportsOwnerReference(restMapper, owner, dependent, "")
				if err != nil {
					return err
				}

				if useOwnerRef { // Setup watch using owner references.
					err = c.Watch(
						source.Kind(
							mgr.GetCache(),
							client.Object(unstructuredObj),
							crthandler.TypedEnqueueRequestForOwner[client.Object](mgr.GetScheme(), mgr.GetRESTMapper(), owner, crthandler.OnlyControllerOwner()),
							predicate.DependentPredicate{}))
					if err != nil {
						return err
					}
					fmt.Println("******** Watch/Owner: " + gvkDependent.String())
				} else { // Setup watch using annotations.
					err = c.Watch(
						source.Kind(
							mgr.GetCache(),
							client.Object(unstructuredObj),
							&libhandler.EnqueueRequestForAnnotation[client.Object]{Type: gvkDependent.GroupKind()},
							predicate.DependentPredicate{}))
					if err != nil {
						return err
					}
					fmt.Println("******** Watch/Annotation: " + gvkDependent.String())
				}
				m.Lock()
				watches[gvkDependent] = struct{}{}
				m.Unlock()
				log.Info("Watching dependent resource", "ownerApiVersion", r.GVK.GroupVersion(),
					"ownerKind", r.GVK.Kind, "apiVersion", gvkDependent.GroupVersion(), "kind", gvkDependent.Kind)
				return nil
			}

			// List is not actually a resource and therefore cannot have a
			// watch on it. The watch will be on the kinds listed in the list
			// and will therefore need to be handled individually.
			listGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "List"}
			if gvk == listGVK {
				errListItem := u.EachListItem(func(obj runtime.Object) error {
					return setWatchOnResource(obj)
				})
				if errListItem != nil {
					return errListItem
				}
			} else {
				err := setWatchOnResource(&u)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	r.releaseHook = releaseHook
}
