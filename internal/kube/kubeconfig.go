package kube

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/poneding/ktx/internal/output"
	"github.com/poneding/ktx/internal/types"
	"github.com/poneding/ktx/internal/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/homedir"
)

var (
	DefaultConfigDir  = filepath.Join(homedir.HomeDir(), ".kube")
	DefaultConfigFile = filepath.Join(DefaultConfigDir, "config")
)

// LoadConfigFromFile loads the kubeconfig from the file
func LoadConfigFromFile(file string) *clientcmdapi.Config {
	config, err := clientcmd.LoadFromFile(file)
	if err != nil {
		output.Fatal("Failed to load kubeconfig from file: %s", err)
	}

	return config
}

// SaveConfigToFile saves the kubeconfig to the file
func SaveConfigToFile(config *clientcmdapi.Config, file string) {
	if err := clientcmd.WriteToFile(*config, file); err != nil {
		output.Fatal("Failed to save kubeconfig to file: %s", err)
	}
}

// PrintConfig prints the kubeconfig
func PrintConfig(config *clientcmdapi.Config) {
	v, er := clientcmd.Write(*config)
	if er != nil {
		output.Fatal("Failed to write kubeconfig: %s", er)
	}
	fmt.Print(string(v))
}

// CheckOrInitConfig checks if the kubeconfig exists, if not, create it
func CheckOrInitConfig() {
	kubeconfigDir := DefaultConfigDir
	kubeconfigFile := DefaultConfigFile

	if _, err := os.Stat(kubeconfigFile); os.IsNotExist(err) {
		if err := os.MkdirAll(kubeconfigDir, 0700); err != nil {
			output.Fatal("Failed to create %s directory: %s", kubeconfigDir, err)
		}

		f, err := os.OpenFile(kubeconfigFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			output.Fatal("Failed to create %s: %s", kubeconfigFile, err)
		}
		defer f.Close()
	}
}

// ListContexts lists all contexts in the kubeconfig
func ListContexts(config *clientcmdapi.Config) []*types.ContextProfile {
	var contexts []*types.ContextProfile
	for contextName, context := range config.Contexts {
		item := &types.ContextProfile{
			Current:   contextName == config.CurrentContext,
			Name:      contextName,
			Cluster:   context.Cluster,
			User:      context.AuthInfo,
			Namespace: util.If(context.Namespace != "", context.Namespace, "default"),
			Server:    config.Clusters[context.Cluster].Server,
		}
		item.Emoji = util.If(item.Current, "✦", " ")
		contexts = append(contexts, item)
	}

	sort.Slice(contexts, func(i, j int) bool {
		return contexts[i].Name < contexts[j].Name
	})

	return contexts
}

// GenerateConfigForServiceAccount generates kubeconfig for service account in the given namespace
func GenerateConfigForServiceAccount(serviceAccount, namespace string) *clientcmdapi.Config {
	CheckOrInitConfig()

	restConfig, err := clientcmd.BuildConfigFromFlags("", DefaultConfigFile)
	if err != nil {
		output.Fatal("Failed to build kubernetes rest config: %s from file %s", err, DefaultConfigFile)
	}

	kubeClientset := Client(DefaultConfigFile)
	sa := GetServiceAccount(kubeClientset, serviceAccount, namespace)

	var secret *v1.Secret
	if len(sa.Secrets) == 0 {
		// Create a new secret for the service account
		secret = CreateServiceAccountTokenSecret(kubeClientset, serviceAccount, namespace)

		for i := 0; i < 3; i++ {
			secret = GetSecret(kubeClientset, secret.Name, namespace)
			if len(secret.Data) > 0 {
				break
			}

			time.Sleep(1 * time.Millisecond)
		}
	} else {
		secret = GetSecret(kubeClientset, sa.Secrets[0].Name, namespace)
	}

	return buildConfigFromSecret(restConfig, secret)
}

// buildConfigFromSecret builds kubeconfig from the secret
func buildConfigFromSecret(kubecfg *rest.Config, secret *v1.Secret) *clientcmdapi.Config {
	var (
		suffix     = rand.String(5)
		cfgContext = "context-" + suffix
		cfgCluster = "cluster-" + suffix
		cfgUser    = "user-" + suffix
	)

	return &clientcmdapi.Config{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: cfgContext,
		Contexts: map[string]*clientcmdapi.Context{
			cfgContext: {
				Cluster:   cfgCluster,
				AuthInfo:  cfgUser,
				Namespace: string(secret.Data["namespace"]),
			},
		},
		Clusters: map[string]*clientcmdapi.Cluster{
			cfgCluster: {
				Server:                   kubecfg.Host,
				CertificateAuthorityData: secret.Data["ca.crt"],
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			cfgUser: {
				Token: string(secret.Data["token"]),
			},
		},
	}
}
