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

package sender_yaml

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cloudevents/conformance/pkg/invoker"
	"github.com/kelseyhightower/envconfig"
	"knative.dev/pkg/logging"
	"knative.dev/reconciler-test/pkg/eventshub"
)

type envConfig struct {
	SenderName string `envconfig:"POD_NAME" default:"sender-default" required:"true"`

	// Sink url for the message destination
	Sink string `envconfig:"SINK" required:"true"`

	// The number of seconds to wait before starting sending the first message
	Delay int `envconfig:"DELAY" default:"5" required:"false"`

	// ProbeSinkTimeout defines the maximum amount of time in seconds to wait for the probe sink to succeed.
	ProbeSinkTimeout int `envconfig:"PROBE_SINK_TIMEOUT" required:"false" default:"60"`

	// The number of seconds between messages.
	Period int `envconfig:"PERIOD" default:"5" required:"false"`

	Input string `envconfig:"EVENTS_YAML"  required:"true"`
}

func Start(ctx context.Context, logs *eventshub.EventLogs) error {
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		return fmt.Errorf("failed to process env var. %w", err)
	}

	logging.FromContext(ctx).Infof("Sender YAML environment configuration: %+v", env)

	period := time.Duration(env.Period) * time.Second
	delay := time.Duration(env.Delay) * time.Second

	if delay > 0 {
		logging.FromContext(ctx).Info("will sleep for ", delay)
		time.Sleep(delay)
		logging.FromContext(ctx).Info("awake, continuing")
	}

	sink, err := url.Parse(env.Sink)
	if err != nil {
		return err
	}

	i := &invoker.Invoker{
		URL:       sink,
		Files:     []string{env.Input},
		Recursive: true,
		Verbose:   true,
		Delay:     &period,
	}

	// TODO: invoker needs a hook to report results back from sending.

	return i.Do()
}

/*



 */
