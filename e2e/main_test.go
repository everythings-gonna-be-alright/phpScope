//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/support/kind"
	"sigs.k8s.io/e2e-framework/third_party/helm"
)

const (
	namespace   = "phpscope-e2e"
	releaseName = "wp"
	image       = "phpscope:e2e"
	chartPath   = "../examples/helm-chart/wordpress-example"

	wordpressURL  = "http://wp-wordpress-example:8080"
	pyroscopeSvc  = "http:wp-pyroscope:4040"
	phpscopeAppID = "wordpress"
)

var (
	testenv     env.Environment
	clusterName = envOr("E2E_CLUSTER_NAME", "phpscope-test")
	keepCluster = os.Getenv("E2E_KEEP_CLUSTER") == "1"
)

func TestMain(m *testing.M) {
	// kind writes the new cluster into $KUBECONFIG and switches the current
	// context, so keep it away from the user's real kubeconfig.
	kubeconfigDir, err := os.MkdirTemp("", "phpscope-e2e-")
	if err != nil {
		log.Fatalf("create kubeconfig dir: %v", err)
	}
	if err := os.Setenv("KUBECONFIG", filepath.Join(kubeconfigDir, "kubeconfig")); err != nil {
		log.Fatalf("set KUBECONFIG: %v", err)
	}

	testenv = env.New()

	testenv.Setup(
		buildImage,
		envfuncs.CreateCluster(kind.NewProvider(), clusterName),
		envfuncs.LoadImageToCluster(clusterName, image),
		installChart,
	)

	var finish []env.Func
	if dir := os.Getenv("E2E_LOGS_DIR"); dir != "" {
		finish = append(finish, envfuncs.ExportClusterLogs(clusterName, dir))
	}
	if !keepCluster {
		finish = append(finish, envfuncs.DestroyCluster(clusterName))
	}
	testenv.Finish(finish...)

	code := testenv.Run(m)
	if err := os.RemoveAll(kubeconfigDir); err != nil {
		log.Printf("remove kubeconfig dir: %v", err)
	}
	os.Exit(code)
}

func buildImage(ctx context.Context, _ *envconf.Config) (context.Context, error) {
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", image, "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("docker build: %w\n%s", err, out)
	}
	return ctx, nil
}

func installChart(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	err := helm.New(cfg.KubeconfigFile()).RunUpgrade(
		helm.WithName(releaseName),
		helm.WithNamespace(namespace),
		helm.WithChart(chartPath),
		helm.WithArgs("--install", "--create-namespace", "--values", "testdata/values.yaml"),
		helm.WithWait(),
		helm.WithTimeout("10m"),
		helm.WithContext(ctx),
	)
	if err != nil {
		return ctx, fmt.Errorf("helm install: %w", err)
	}
	return ctx, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
