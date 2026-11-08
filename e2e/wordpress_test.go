//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const (
	cpuProfile     = "process_cpu:cpu:nanoseconds:cpu:nanoseconds"
	samplesProfile = "process_cpu:samples:count:cpu:nanoseconds"
	selector       = `{service_name="` + phpscopeAppID + `",env="e2e",app="wordpress"}`
	minSamples     = 100
)

const installScript = `
curl -fsS -o /dev/null "$BASE/wp-admin/install.php?step=2" \
  --data-urlencode "weblog_title=phpScope e2e" \
  --data-urlencode "user_name=admin" \
  --data-urlencode "admin_password=$ADMIN_PASSWORD" \
  --data-urlencode "admin_password2=$ADMIN_PASSWORD" \
  --data-urlencode "pw_weak=1" \
  --data-urlencode "admin_email=e2e@example.com" \
  --data-urlencode "blog_public=0"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/wp-login.php")
[ "$code" = 200 ] || { echo "wp-login.php returned $code after install"; exit 1; }
echo "installed"
`

// Every request carries a unique e2e_req id and logs "req <id> <start> <end>"
// in epoch seconds. Idle rounds come first: requests made after a pause are
// where stale timestamps show up.
const trafficScript = `
n=0
get() {
  n=$((n+1))
  case "$1" in *\?*) url="$BASE$1&e2e_req=$n" ;; *) url="$BASE$1?e2e_req=$n" ;; esac
  start=$(date +%s)
  code=$(curl -s -o /dev/null -w '%{http_code}' "$url")
  end=$(date +%s)
  [ "$code" = 200 ] || { echo "GET $url returned $code"; exit 1; }
  echo "req $n $start $end"
}
for i in $(seq 1 "$IDLE_ROUNDS"); do
  sleep 3
  get "/?p=1"
done
for i in $(seq 1 "$ROUNDS"); do
  for path in "/" "/?p=1" "/?s=hello" "/?feed=rss2" "/?rest_route=/wp/v2/posts" "/wp-login.php"; do
    get "$path"
  done
done
echo "requests=$n"
`

// Profiles are batched every ~100ms and queried at 100ms resolution.
const timeSlack = time.Second

// Some requests are legitimately unprofiled: a request shorter than a few
// sampling periods can fall between samples, and phpspy's --pgrep mode rescans
// for new PHP-FPM workers only every 2s, so a freshly spawned worker is blind
// until it is attached.
const minRequestCoverage = 0.90

// Functions every front-end WordPress request executes.
var expectedFunctions = []string{"WP::main", "wpdb::query", "apply_filters"}

const indexPHP = "/var/www/html/index.php"

// Each endpoint in trafficScript, how to select its samples, and a function
// only that endpoint executes.
var endpoints = []struct {
	name, path, qstring, marker string
}{
	{"/", indexPHP, `e2e_req=[0-9]+`, "get_home_template"},
	{"/?p=1", indexPHP, `p=1&e2e_req=[0-9]+`, "get_single_template"},
	{"/?s=hello", indexPHP, `s=hello&e2e_req=[0-9]+`, "get_search_template"},
	{"/?feed=rss2", indexPHP, `feed=rss2&e2e_req=[0-9]+`, "do_feed_rss2"},
	{"/?rest_route=/wp/v2/posts", indexPHP, `rest_route=/wp/v2/posts&e2e_req=[0-9]+`, "WP_REST_Server::serve_request"},
	{"/wp-login.php", "/var/www/html/wp-login.php", `e2e_req=[0-9]+`, "login_header"},
}

// phpspy reads the stack and the request info at slightly different moments,
// so a sample can rarely carry the labels of the request before or after it.
const maxMislabeledShare = 0.01

// Trace fields left out of sampleLabels in testdata/values.yaml.
var disallowedLabels = []string{"cookie", "uri", "mem"}

// Frames that mean WordPress failed while serving traffic.
var errorFunctions = []string{"wpdb::print_error", "wp_die"}

var (
	logErrorRe      = regexp.MustCompile(`(?i)\b(error|panic|fatal)\b`)
	phpspyRunningRe = regexp.MustCompile(`Starting process: \S*phpspy`)
	requestLineRe   = regexp.MustCompile(`(?m)^req (\d+) (\d+) (\d+)$`)
)

type request struct {
	id         string
	start, end time.Time
}

func TestWordPressProfiling(t *testing.T) {
	var (
		testStart    = time.Now()
		trafficStart time.Time
		requests     []request
	)

	feature := features.New("phpScope profiles WordPress into Pyroscope").
		Assess("WordPress installs", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			runCurlJob(ctx, t, cfg, "wp-install", installScript, map[string]string{"ADMIN_PASSWORD": rand.Text()})
			return ctx
		}).
		Assess("traffic is served without errors", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			trafficStart = time.Now()
			logs := runCurlJob(ctx, t, cfg, "wp-traffic", trafficScript, map[string]string{"IDLE_ROUNDS": "5", "ROUNDS": "200"})
			requests = parseRequests(t, logs)
			t.Logf("requests=%d", len(requests))
			return ctx
		}).
		Assess("profiles reach Pyroscope with tags", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			requireTraffic(t, trafficStart)
			from := trafficStart.Add(-time.Minute)

			for label, want := range map[string]string{"service_name": phpscopeAppID, "env": "e2e", "app": "wordpress"} {
				var got []string
				eventually(t, 2*time.Minute, func() (bool, string) {
					var err error
					got, err = labelValues(ctx, t, cfg, label, from, time.Now())
					return err == nil && slices.Contains(got, want), "label " + label + " values " + join(got) + errString(err)
				})
			}

			names, err := labelNames(ctx, t, cfg, selector, from, time.Now())
			if err != nil {
				t.Fatalf("label names: %v", err)
			}
			for _, l := range disallowedLabels {
				if slices.Contains(names, l) {
					t.Errorf("label %q is not in sampleLabels but was sent to Pyroscope; labels: %s", l, join(names))
				}
			}
			return ctx
		}).
		Assess("every request is profiled at the time it was made", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			requireTraffic(t, trafficStart)
			reqSelector := strings.TrimSuffix(selector, "}") + `,qstring=~".*e2e_req=.*"}`

			var byID map[string][]requestSeries
			var missing []string
			prevMissing := -1
			eventually(t, 2*time.Minute, func() (bool, string) {
				series, err := selectRequestSeries(ctx, t, cfg, reqSelector, trafficStart.Add(-10*time.Second), time.Now())
				if err != nil {
					return false, err.Error()
				}
				byID = map[string][]requestSeries{}
				for _, s := range series {
					q, err := url.ParseQuery(s.QString)
					if err != nil {
						return false, err.Error()
					}
					byID[q.Get("e2e_req")] = append(byID[q.Get("e2e_req")], s)
				}
				missing = nil
				for _, r := range requests {
					if len(byID[r.id]) == 0 {
						missing = append(missing, r.id)
					}
				}
				settled := len(missing) == prevMissing && len(missing) < len(requests)
				prevMissing = len(missing)
				return settled, fmt.Sprintf("%d of %d requests have no samples yet", len(missing), len(requests))
			})

			coverage := 1 - float64(len(missing))/float64(len(requests))
			if coverage < minRequestCoverage {
				t.Errorf("only %.1f%% of requests were profiled, want at least %.0f%%; not profiled: e2e_req=%s",
					coverage*100, minRequestCoverage*100, join(first(missing, 20)))
			}

			var bad []string
			var worst time.Duration
			for _, r := range requests {
				for _, s := range byID[r.id] {
					start := s.Start
					switch {
					case start.Unix() <= 0:
						// PHP sets the request time lazily; phpspy can sample before that.
						start = r.start
					case start.Before(r.start) || start.After(r.end.Add(time.Second)):
						bad = append(bad, fmt.Sprintf("e2e_req=%s: PHP saw the request at %s, client sent it during [%s, %s]",
							r.id, start.Format(time.RFC3339Nano), r.start.Format(time.RFC3339), r.end.Format(time.RFC3339)))
					}
					for _, p := range s.Points {
						if p.Before(start.Add(-timeSlack)) || p.After(r.end.Add(time.Second+timeSlack)) {
							bad = append(bad, fmt.Sprintf("e2e_req=%s: samples stored at %s, request started at %s (%+v)",
								r.id, p.Format(time.RFC3339Nano), start.Format(time.RFC3339Nano), p.Sub(start)))
						}
						worst = max(worst, p.Sub(start).Abs())
					}
				}
			}
			if len(bad) > 0 {
				t.Errorf("%d samples stored at the wrong time:\n%s", len(bad), strings.Join(first(bad, 10), "\n"))
			}
			t.Logf("%d of %d requests profiled (%.1f%%), samples stored within %v of request start; not profiled: e2e_req=%s",
				len(requests)-len(missing), len(requests), coverage*100, worst, join(first(missing, 20)))
			return ctx
		}).
		Assess("each endpoint's samples show that endpoint's code", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			requireTraffic(t, trafficStart)
			from := trafficStart.Add(-10 * time.Second)

			for _, ep := range endpoints {
				sel := fmt.Sprintf(`{service_name=%q,env="e2e",app="wordpress",path=%q,qstring=~%q}`, phpscopeAppID, ep.path, ep.qstring)
				fg, err := mergedFlamegraph(ctx, t, cfg, samplesProfile, sel, from, time.Now())
				if err != nil {
					t.Fatalf("%s: %v", ep.name, err)
				}
				if fg.Total == 0 {
					t.Errorf("%s: no samples", ep.name)
					continue
				}
				if fg.Samples[ep.marker] == 0 {
					t.Errorf("%s: no sample contains %s", ep.name, ep.marker)
				}

				var foreign int64
				var foreignFns []string
				for _, other := range endpoints {
					if n := fg.Samples[other.marker]; other.marker != ep.marker && n > 0 {
						foreign += n
						foreignFns = append(foreignFns, fmt.Sprintf("%s=%d", other.marker, n))
					}
				}
				if float64(foreign) > maxMislabeledShare*float64(fg.Total) {
					t.Errorf("%s: %d of %d samples show other endpoints' code (%s)", ep.name, foreign, fg.Total, strings.Join(foreignFns, ", "))
				}
				t.Logf("%-27s samples=%-5d %s=%d foreign=%d", ep.name, fg.Total, ep.marker, fg.Samples[ep.marker], foreign)
			}
			return ctx
		}).
		Assess("profiles contain WordPress stacks and no error frames", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			requireTraffic(t, trafficStart)
			// A second of slack keeps batches that started before the traffic job out of the window.
			from := trafficStart.Add(time.Second)

			var fg flamegraph
			eventually(t, 2*time.Minute, func() (bool, string) {
				var err error
				fg, err = mergedFlamegraph(ctx, t, cfg, cpuProfile, selector, from, time.Now())
				return err == nil && containsAll(fg.Names, expectedFunctions), "flamegraph missing " + join(missing(fg.Names, expectedFunctions)) + errString(err)
			})

			for _, fn := range errorFunctions {
				if slices.Contains(fg.Names, fn) {
					t.Errorf("error frame %q found in profiles captured during traffic", fn)
				}
			}

			samples, err := mergedFlamegraph(ctx, t, cfg, samplesProfile, selector, from, time.Now())
			if err != nil {
				t.Fatalf("samples query: %v", err)
			}
			if samples.Total < minSamples {
				t.Errorf("only %d samples ingested, want at least %d", samples.Total, minSamples)
			}
			t.Logf("ingested %d samples across %d distinct frames", samples.Total, len(fg.Names))
			return ctx
		}).
		Assess("phpScope logs no errors and nothing restarted", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var pods corev1.PodList
			if err := cfg.Client().Resources(namespace).List(ctx, &pods); err != nil {
				t.Fatalf("list pods: %v", err)
			}

			for _, pod := range pods.Items {
				if pod.Labels["batch.kubernetes.io/job-name"] != "" {
					continue
				}
				for _, cs := range pod.Status.ContainerStatuses {
					if cs.RestartCount > 0 {
						t.Errorf("container %s/%s restarted %d times", pod.Name, cs.Name, cs.RestartCount)
					}
				}
				if pod.Labels["app.kubernetes.io/component"] != "wordpress" {
					continue
				}
				if logs := containerLogs(ctx, t, cfg, pod.Name, "phpscope", time.Time{}); !phpspyRunningRe.MatchString(logs) {
					t.Errorf("phpspy was not started:\n%s", logs)
				}
				// Sends can fail while Pyroscope is still starting; only errors
				// after the release reported ready count.
				logs := containerLogs(ctx, t, cfg, pod.Name, "phpscope", testStart)
				for _, line := range strings.Split(logs, "\n") {
					if logErrorRe.MatchString(line) {
						t.Errorf("phpscope logged an error: %s", line)
					}
				}
			}
			return ctx
		}).
		Feature()

	testenv.Test(t, feature)
}

func requireTraffic(t *testing.T, start time.Time) {
	t.Helper()
	if start.IsZero() {
		t.Skip("traffic was not generated")
	}
}

func eventually(t *testing.T, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ok, msg := cond()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v: %s", timeout, msg)
		}
		time.Sleep(3 * time.Second)
	}
}

func parseRequests(t *testing.T, logs string) []request {
	t.Helper()
	var out []request
	for _, m := range requestLineRe.FindAllStringSubmatch(logs, -1) {
		start, _ := strconv.ParseInt(m[2], 10, 64)
		end, _ := strconv.ParseInt(m[3], 10, 64)
		out = append(out, request{id: m[1], start: time.Unix(start, 0), end: time.Unix(end, 0)})
	}
	if len(out) == 0 {
		t.Fatalf("no requests in traffic log:\n%s", logs)
	}
	return out
}

func first[T any](s []T, n int) []T { return s[:min(n, len(s))] }

func containsAll(have, want []string) bool { return len(missing(have, want)) == 0 }

func missing(have, want []string) []string {
	var out []string
	for _, w := range want {
		if !slices.Contains(have, w) {
			out = append(out, w)
		}
	}
	return out
}

func join(s []string) string { return "[" + strings.Join(s, ", ") + "]" }

func errString(err error) string {
	if err == nil {
		return ""
	}
	return " (" + err.Error() + ")"
}
