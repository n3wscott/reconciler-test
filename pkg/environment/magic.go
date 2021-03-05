/*
Copyright 2020 The Knative Authors

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

package environment

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/logging"
	"knative.dev/reconciler-test/pkg/milestone"

	"knative.dev/reconciler-test/pkg/feature"
	"knative.dev/reconciler-test/pkg/state"
)

func NewGlobalEnvironment(ctx context.Context) GlobalEnvironment {
	fmt.Printf("level %s, state %s\n\n", l, s)

	return &MagicGlobalEnvironment{
		c:                ctx,
		instanceID:       uuid.New().String(),
		RequirementLevel: *l,
		FeatureState:     *s,
	}
}

type MagicGlobalEnvironment struct {
	c context.Context
	// instanceID represents this instance of the GlobalEnvironment. It is used
	// to link runs together from a single global environment.
	instanceID string

	RequirementLevel feature.Levels
	FeatureState     feature.States
}

type MagicEnvironment struct {
	c context.Context
	l feature.Levels
	s feature.States

	images           map[string]string
	namespace        string
	namespaceCreated bool
	refs             []corev1.ObjectReference
	// milestones sends milestone events, if configured.
	milestones milestone.Sender
}

const (
	NamespaceDeleteErrorReason = "NamespaceDeleteError"
)

func (mr *MagicEnvironment) Reference(ref ...corev1.ObjectReference) {
	mr.refs = append(mr.refs, ref...)
}

func (mr *MagicEnvironment) References() []corev1.ObjectReference {
	return mr.refs
}

func (mr *MagicEnvironment) Finish() {
	if err := mr.DeleteNamespaceIfNeeded(); err != nil {
		mr.milestones.Exception(NamespaceDeleteErrorReason, "failed to delete namespace %q, %v", mr.namespace, err)
		panic(err)
	}
	mr.milestones.Finished()
}

func (mr *MagicGlobalEnvironment) Environment(opts ...EnvOpts) (context.Context, Environment) {
	images, err := ProduceImages()
	if err != nil {
		panic(err)
	}

	namespace := feature.MakeK8sNamePrefix(feature.AppendRandomString("rekt"))

	env := &MagicEnvironment{
		c:         mr.c,
		l:         mr.RequirementLevel,
		s:         mr.FeatureState,
		images:    images,
		namespace: namespace,
	}

	ctx := ContextWith(mr.c, env)

	for _, opt := range opts {
		if ctx, err = opt(ctx, env); err != nil {
			panic(err)
		}
	}

	// It is possible to have milestones set in the options, check for nil in
	// env first before attempting to pull one from the os environment.
	if env.milestones == nil {
		milestones, err := milestone.NewMilestoneEventSenderFromEnv(mr.instanceID, namespace)
		if err != nil {
			// This is just an FYI error, don't block the test run.
			logging.FromContext(mr.c).Error("failed to create the milestone event sender", zap.Error(err))
		}
		if milestones != nil {
			env.milestones = milestones
		}
	}

	if err := env.CreateNamespaceIfNeeded(); err != nil {
		panic(err)
	}

	env.milestones.Environment(map[string]string{
		// TODO: we could add more detail here, don't send secrets.
		"requirementLevel": env.RequirementLevel().String(),
		"featureState":     env.FeatureState().String(),
		"namespace":        env.Namespace(),
	})

	return ctx, env
}

func (mr *MagicEnvironment) Images() map[string]string {
	return mr.images
}

func (mr *MagicEnvironment) TemplateConfig(base map[string]interface{}) map[string]interface{} {
	cfg := make(map[string]interface{})
	for k, v := range base {
		cfg[k] = v
	}
	cfg["namespace"] = mr.namespace
	return cfg
}

func (mr *MagicEnvironment) RequirementLevel() feature.Levels {
	return mr.l
}

func (mr *MagicEnvironment) FeatureState() feature.States {
	return mr.s
}

func (mr *MagicEnvironment) Namespace() string {
	return mr.namespace
}

func (mr *MagicEnvironment) Prerequisite(ctx context.Context, t *testing.T, f *feature.Feature) {
	t.Helper() // Helper marks the calling function as a test helper function.
	t.Run("Prerequisite", func(t *testing.T) {
		mr.Test(ctx, t, f)
	})
}

// Test implements Environment.Test.
// In the MagicEnvironment implementation, the Store that is inside of the
// Feature will be assigned to the context. If no Store is set on Feature,
// Test will create a new store.KVStore and set it on the feature and then
// apply it to the Context.
func (mr *MagicEnvironment) Test(ctx context.Context, t *testing.T, f *feature.Feature) {
	t.Helper() // Helper marks the calling function as a test helper function.

	if f.State == nil {
		f.State = &state.KVStore{}
	}
	ctx = state.ContextWith(ctx, f.State)

	steps := feature.CollapseSteps(f.Steps)

	for _, timing := range feature.Timings() {
		wg := &sync.WaitGroup{}
		wg.Add(1)

		t.Run(timing.String(), func(t *testing.T) {
			t.Helper()      // Helper marks the calling function as a test helper function.
			defer wg.Done() // Outer wait.

			for _, s := range steps {
				// Skip if step phase is not running.
				if s.T != timing {
					continue
				}
				t.Run(s.TestName(), func(t *testing.T) {
					ctx, cancelFn := context.WithCancel(ctx)
					t.Cleanup(cancelFn)

					wg.Add(1)
					defer func() {
						// Send result milestone event.
						mr.milestones.TestFinished(f.Name, s.TestName(), t.Name(), t.Skipped(), t.Failed())

						wg.Done()
					}()

					if mr.s&s.S == 0 {
						t.Skipf("%s features not enabled for testing", s.S)
					}
					if mr.l&s.L == 0 {
						t.Skipf("%s requirement not enabled for testing", s.L)
					}

					s := s

					t.Helper() // Helper marks the calling function as a test helper function.

					// Send starting milestone event.
					mr.milestones.TestStarted(f.Name, s.TestName(), t.Name())

					// Perform step.
					s.Fn(ctx, t)
				})
			}
		})

		wg.Wait()
	}
}

// TestSet implements Environment.TestSet
func (mr *MagicEnvironment) TestSet(ctx context.Context, t *testing.T, fs *feature.FeatureSet) {
	t.Helper() // Helper marks the calling function as a test helper function
	wg := &sync.WaitGroup{}

	mr.milestones.TestSetStarted(fs.Name, t.Name())

	for _, f := range fs.Features {
		wg.Add(1)
		t.Run(fs.Name, func(t *testing.T) {
			// FeatureSets should be run in parellel.
			mr.Test(ctx, t, &f)
			wg.Done()
		})
	}

	wg.Wait()

	// Send result milestone event.
	mr.milestones.TestSetFinished(fs.Name, t.Name(), t.Skipped(), t.Failed())

}

type envKey struct{}

func ContextWith(ctx context.Context, e Environment) context.Context {
	return context.WithValue(ctx, envKey{}, e)
}

func FromContext(ctx context.Context) Environment {
	if e, ok := ctx.Value(envKey{}).(Environment); ok {
		return e
	}
	panic("no Environment found in context")
}
