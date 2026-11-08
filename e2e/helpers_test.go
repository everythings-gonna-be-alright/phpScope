//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
)

const curlImage = "curlimages/curl:8.23.0"

func clientset(t *testing.T, cfg *envconf.Config) *kubernetes.Clientset {
	t.Helper()
	cs, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
	if err != nil {
		t.Fatalf("kubernetes client: %v", err)
	}
	return cs
}

// runCurlJob runs script in a hardened curl pod inside the release namespace,
// so traffic goes through the chart's NetworkPolicy like a real client would.
func runCurlJob(ctx context.Context, t *testing.T, cfg *envconf.Config, name, script string, env map[string]string) string {
	t.Helper()

	var envVars []corev1.EnvVar
	for k, v := range env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}
	envVars = append(envVars, corev1.EnvVar{Name: "BASE", Value: wordpressURL})

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{GenerateName: name + "-", Namespace: namespace},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To[int32](0),
			TTLSecondsAfterFinished: ptr.To[int32](600),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptr.To(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						RunAsUser:      ptr.To[int64](100),
						RunAsGroup:     ptr.To[int64](101),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:    "curl",
						Image:   curlImage,
						Command: []string{"sh", "-ec", script},
						Env:     envVars,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							ReadOnlyRootFilesystem:   ptr.To(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}

	r := cfg.Client().Resources()
	if err := r.Create(ctx, job); err != nil {
		t.Fatalf("create job %s: %v", name, err)
	}
	name = job.Name

	err := wait.For(conditions.New(r).ResourceMatch(job, func(o k8s.Object) bool {
		j := o.(*batchv1.Job)
		return j.Status.Succeeded > 0 || j.Status.Failed > 0
	}), wait.WithTimeout(10*time.Minute), wait.WithInterval(2*time.Second), wait.WithContext(ctx))
	logs := jobLogs(ctx, t, cfg, name)
	if err != nil {
		t.Fatalf("job %s did not finish: %v\n%s", name, err, logs)
	}
	if job.Status.Failed > 0 {
		t.Fatalf("job %s failed:\n%s", name, logs)
	}
	return logs
}

func jobLogs(ctx context.Context, t *testing.T, cfg *envconf.Config, job string) string {
	t.Helper()
	cs := clientset(t, cfg)
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "batch.kubernetes.io/job-name=" + job})
	if err != nil || len(pods.Items) == 0 {
		return fmt.Sprintf("<no logs: %v>", err)
	}
	raw, err := cs.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{}).DoRaw(ctx)
	if err != nil {
		return fmt.Sprintf("<no logs: %v>", err)
	}
	return string(raw)
}

// containerLogs returns the container's logs, only those written after since
// unless since is zero.
func containerLogs(ctx context.Context, t *testing.T, cfg *envconf.Config, pod, container string, since time.Time) string {
	t.Helper()
	opts := &corev1.PodLogOptions{Container: container}
	if !since.IsZero() {
		opts.SinceTime = &metav1.Time{Time: since}
	}
	raw, err := clientset(t, cfg).CoreV1().Pods(namespace).GetLogs(pod, opts).DoRaw(ctx)
	if err != nil {
		t.Fatalf("logs %s/%s: %v", pod, container, err)
	}
	return string(raw)
}

// pyroscopeQuery calls a Pyroscope Connect querier method through the
// Kubernetes API server service proxy, so no port-forward is needed.
func pyroscopeQuery(ctx context.Context, t *testing.T, cfg *envconf.Config, method string, req, resp any) error {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	raw, err := clientset(t, cfg).CoreV1().RESTClient().Post().
		AbsPath("/api/v1/namespaces", namespace, "services", pyroscopeSvc, "proxy", "querier.v1.QuerierService", method).
		SetHeader("Content-Type", "application/json").
		Body(body).
		DoRaw(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w: %s", method, err, raw)
	}
	return json.NewDecoder(bytes.NewReader(raw)).Decode(resp)
}

func labelNames(ctx context.Context, t *testing.T, cfg *envconf.Config, matcher string, from, to time.Time) ([]string, error) {
	var resp struct {
		Names []string `json:"names"`
	}
	err := pyroscopeQuery(ctx, t, cfg, "LabelNames", map[string]any{
		"matchers": []string{matcher},
		"start":    from.UnixMilli(),
		"end":      to.UnixMilli(),
	}, &resp)
	return resp.Names, err
}

func labelValues(ctx context.Context, t *testing.T, cfg *envconf.Config, label string, from, to time.Time) ([]string, error) {
	var resp struct {
		Names []string `json:"names"`
	}
	err := pyroscopeQuery(ctx, t, cfg, "LabelValues", map[string]any{
		"name":  label,
		"start": from.UnixMilli(),
		"end":   to.UnixMilli(),
	}, &resp)
	return resp.Names, err
}

// requestSeries is the profile data of one PHP request, identified by the
// qstring and ts (request start time) labels phpspy attaches to each sample.
type requestSeries struct {
	QString string
	Start   time.Time
	Points  []time.Time
}

func selectRequestSeries(ctx context.Context, t *testing.T, cfg *envconf.Config, selector string, from, to time.Time) ([]requestSeries, error) {
	var resp struct {
		Series []struct {
			Labels []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"labels"`
			Points []struct {
				Timestamp string `json:"timestamp"`
			} `json:"points"`
		} `json:"series"`
	}
	err := pyroscopeQuery(ctx, t, cfg, "SelectSeries", map[string]any{
		"profile_typeID": samplesProfile,
		"label_selector": selector,
		"start":          from.UnixMilli(),
		"end":            to.UnixMilli(),
		"group_by":       []string{"qstring", "ts"},
		"step":           0.1,
	}, &resp)
	if err != nil {
		return nil, err
	}

	out := make([]requestSeries, 0, len(resp.Series))
	for _, s := range resp.Series {
		var rs requestSeries
		for _, l := range s.Labels {
			switch l.Name {
			case "qstring":
				rs.QString = l.Value
			case "ts":
				sec, err := strconv.ParseFloat(l.Value, 64)
				if err != nil {
					return nil, fmt.Errorf("ts label %q: %w", l.Value, err)
				}
				rs.Start = time.Unix(0, int64(sec*float64(time.Second)))
			}
		}
		for _, p := range s.Points {
			ms, err := strconv.ParseInt(p.Timestamp, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("point timestamp %q: %w", p.Timestamp, err)
			}
			rs.Points = append(rs.Points, time.UnixMilli(ms))
		}
		out = append(out, rs)
	}
	return out, nil
}

// jsonInt64 decodes protojson int64 values, which are encoded as quoted strings.
type jsonInt64 int64

func (n *jsonInt64) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(strings.Trim(string(b), `"`), 10, 64)
	*n = jsonInt64(v)
	return err
}

type flamegraph struct {
	Names []string
	Total int64
	// Samples counts, per function, the samples whose stack contains it.
	Samples map[string]int64
}

func mergedFlamegraph(ctx context.Context, t *testing.T, cfg *envconf.Config, profileType, selector string, from, to time.Time) (flamegraph, error) {
	var resp struct {
		Flamegraph struct {
			Names  []string  `json:"names"`
			Total  jsonInt64 `json:"total"`
			Levels []struct {
				Values []jsonInt64 `json:"values"`
			} `json:"levels"`
		} `json:"flamegraph"`
	}
	err := pyroscopeQuery(ctx, t, cfg, "SelectMergeStacktraces", map[string]any{
		"profile_typeID": profileType,
		"label_selector": selector,
		"start":          from.UnixMilli(),
		"end":            to.UnixMilli(),
		"format":         "PROFILE_FORMAT_FLAMEGRAPH",
		"max_nodes":      0,
	}, &resp)
	if err != nil {
		return flamegraph{}, err
	}

	fg := resp.Flamegraph
	out := flamegraph{Names: fg.Names, Total: int64(fg.Total), Samples: map[string]int64{}}
	// Each level holds nodes as (offset, total, self, name index) quadruples.
	for _, level := range fg.Levels {
		for i := 0; i+3 < len(level.Values); i += 4 {
			if idx := int(level.Values[i+3]); idx >= 0 && idx < len(fg.Names) {
				out.Samples[fg.Names[idx]] += int64(level.Values[i+1])
			}
		}
	}
	return out, nil
}
