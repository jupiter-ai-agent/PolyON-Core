package kube

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/module"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// DeployModule creates K8s resources based on module.yaml spec.
// prcEnv contains resolved PRC credentials (env key → value). May be nil.
func (c *Client) DeployModule(ctx context.Context, moduleID string, spec module.Spec, prcEnv map[string]string) error {
	if c == nil || c.cs == nil {
		return fmt.Errorf("k8s client not initialized")
	}

	log.Info().Str("module_id", moduleID).Msg("Starting K8s deployment")

	// 1. Create Secret (PRC credentials + custom secrets)
	if err := c.createModuleSecret(ctx, moduleID, spec, prcEnv); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	// 2. Create PVCs if defined
	if len(spec.Resources.PVC) > 0 {
		if err := c.createModulePVCs(ctx, moduleID, spec.Resources.PVC); err != nil {
			return fmt.Errorf("failed to create PVCs: %w", err)
		}
	}

	// 3. Create Service
	if err := c.createModuleService(ctx, moduleID, spec.Resources.Ports); err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	// 4. Create Deployment or StatefulSet
	if spec.Resources.StatefulSet {
		if err := c.createModuleStatefulSet(ctx, moduleID, spec.Resources); err != nil {
			return fmt.Errorf("failed to create statefulset: %w", err)
		}
	} else {
		if err := c.createModuleDeployment(ctx, moduleID, spec.Resources); err != nil {
			return fmt.Errorf("failed to create deployment: %w", err)
		}
	}

	// 5. Create Ingress if configured (subdomain or path prefix)
	if spec.Ingress.Subdomain != "" || spec.Ingress.PathPrefix != "" {
		if err := c.createModuleIngress(ctx, moduleID, spec.Ingress); err != nil {
			return fmt.Errorf("failed to create ingress: %w", err)
		}
	}

	log.Info().Str("module_id", moduleID).Msg("K8s deployment completed")
	return nil
}

// DeleteModule removes all K8s resources for a module.
func (c *Client) DeleteModule(ctx context.Context, moduleID string) error {
	if c == nil || c.cs == nil {
		return fmt.Errorf("k8s client not initialized")
	}

	log.Info().Str("module_id", moduleID).Msg("Starting K8s resource cleanup")

	// Delete in reverse order (Ingress → Deployment → Service → PVC → Secret)
	
	// 1. Delete Ingress
	ingressName := fmt.Sprintf("polyon-%s", moduleID)
	if err := c.cs.NetworkingV1().Ingresses(c.namespace).Delete(ctx, ingressName, metav1.DeleteOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			log.Warn().Err(err).Str("module_id", moduleID).Msg("Failed to delete ingress")
		}
	}

	// 2. Delete Deployment
	deploymentName := fmt.Sprintf("polyon-%s", moduleID)
	if err := c.cs.AppsV1().Deployments(c.namespace).Delete(ctx, deploymentName, metav1.DeleteOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			log.Warn().Err(err).Str("module_id", moduleID).Msg("Failed to delete deployment")
		}
	}

	// 3. Delete StatefulSet (if exists)
	if err := c.cs.AppsV1().StatefulSets(c.namespace).Delete(ctx, deploymentName, metav1.DeleteOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			log.Warn().Err(err).Str("module_id", moduleID).Msg("Failed to delete statefulset")
		}
	}

	// 4. Delete Service
	serviceName := fmt.Sprintf("polyon-%s", moduleID)
	if err := c.cs.CoreV1().Services(c.namespace).Delete(ctx, serviceName, metav1.DeleteOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			log.Warn().Err(err).Str("module_id", moduleID).Msg("Failed to delete service")
		}
	}

	// 5. Delete Secret
	secretName := fmt.Sprintf("polyon-module-%s", moduleID)
	if err := c.cs.CoreV1().Secrets(c.namespace).Delete(ctx, secretName, metav1.DeleteOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			log.Warn().Err(err).Str("module_id", moduleID).Msg("Failed to delete secret")
		}
	}

	// 6. Delete PVCs (label selector)
	pvcList, err := c.cs.CoreV1().PersistentVolumeClaims(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("polyon.io/module=%s", moduleID),
	})
	if err == nil {
		for _, pvc := range pvcList.Items {
			if err := c.cs.CoreV1().PersistentVolumeClaims(c.namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
				log.Warn().Err(err).Str("pvc", pvc.Name).Msg("Failed to delete PVC")
			} else {
				log.Info().Str("pvc", pvc.Name).Msg("PVC deleted")
			}
		}
	}
	// Also try name-based cleanup
	pvcPrefix := fmt.Sprintf("polyon-%s-", moduleID)
	allPvcs, _ := c.cs.CoreV1().PersistentVolumeClaims(c.namespace).List(ctx, metav1.ListOptions{})
	if allPvcs != nil {
		for _, pvc := range allPvcs.Items {
			if len(pvc.Name) > len(pvcPrefix) && pvc.Name[:len(pvcPrefix)] == pvcPrefix {
				if err := c.cs.CoreV1().PersistentVolumeClaims(c.namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
					if !errors.IsNotFound(err) {
						log.Warn().Err(err).Str("pvc", pvc.Name).Msg("Failed to delete PVC by name")
					}
				} else {
					log.Info().Str("pvc", pvc.Name).Msg("PVC deleted by name match")
				}
			}
		}
	}

	log.Info().Str("module_id", moduleID).Msg("K8s resource cleanup completed")
	return nil
}

// WaitForDeploymentReady waits for a Deployment or StatefulSet to be ready.
func (c *Client) WaitForDeploymentReady(ctx context.Context, moduleID string, timeout time.Duration) error {
	if c == nil || c.cs == nil {
		return fmt.Errorf("k8s client not initialized")
	}

	name := fmt.Sprintf("polyon-%s", moduleID)
	
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	log.Info().Str("module_id", moduleID).Dur("timeout", timeout).Msg("Waiting for workload readiness")

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s to be ready", name)
		default:
			// Try Deployment first
			deployment, err := c.cs.AppsV1().Deployments(c.namespace).Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
					log.Info().Str("module_id", moduleID).Msg("Deployment is ready")
					return nil
				}
				time.Sleep(2 * time.Second)
				continue
			}

			// Try StatefulSet
			sts, stsErr := c.cs.AppsV1().StatefulSets(c.namespace).Get(ctx, name, metav1.GetOptions{})
			if stsErr == nil {
				if sts.Status.ReadyReplicas == *sts.Spec.Replicas {
					log.Info().Str("module_id", moduleID).Msg("StatefulSet is ready")
					return nil
				}
				time.Sleep(2 * time.Second)
				continue
			}

			return fmt.Errorf("no Deployment or StatefulSet found for %s", name)
		}
	}
}

// createModuleSecret creates a K8s Secret for the module.
// prcEnv contains resolved PRC environment variables (may be nil for legacy modules).
func (c *Client) createModuleSecret(ctx context.Context, moduleID string, spec module.Spec, prcEnv map[string]string) error {
	secretName := fmt.Sprintf("polyon-module-%s", moduleID)

	data := make(map[string][]byte)

	// PRC path: inject resolved credentials
	if len(prcEnv) > 0 {
		for k, v := range prcEnv {
			data[k] = []byte(v)
		}
		log.Info().Str("module_id", moduleID).Int("keys", len(prcEnv)).Msg("PRC credentials injected into secret")
	}

	// Legacy path: database connection from spec.database
	if len(prcEnv) == 0 && spec.Database.Create {
		dbHost := "polyon-db"
		dbPort := "5432"
		dbName := spec.Database.Name
		dbUser := spec.Database.User
		dbPassword := "placeholder_will_be_updated"
		dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			dbUser, dbPassword, dbHost, dbPort, dbName)
		data["DATABASE_URL"] = []byte(dbURL)
		data["DB_HOST"] = []byte(dbHost)
		data["DB_PORT"] = []byte(dbPort)
		data["DB_NAME"] = []byte(dbName)
		data["DB_USER"] = []byte(dbUser)
		data["DB_PASSWORD"] = []byte(dbPassword)
	}

	// Auto-generate values for secretKeyRef references
	for _, env := range spec.Resources.Env {
		if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
			ref := env.ValueFrom.SecretKeyRef
			if ref.Name == secretName {
				if _, exists := data[ref.Key]; !exists {
					randBytes := make([]byte, 24)
					rand.Read(randBytes)
					data[ref.Key] = []byte(fmt.Sprintf("%x", randBytes)[:24])
					log.Info().Str("key", ref.Key).Msg("Auto-generated secret key value")
				}
			}
		} else if env.Value != "" {
			// Only add static env if not already set by PRC
			if _, exists := data[env.Name]; !exists {
				data[env.Name] = []byte(env.Value)
			}
		}
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: c.namespace,
			Labels: map[string]string{
				"app":             fmt.Sprintf("polyon-%s", moduleID),
				"polyon.io/module": moduleID,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}

	_, err := c.cs.CoreV1().Secrets(c.namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create secret %s: %w", secretName, err)
	}

	log.Info().Str("secret_name", secretName).Msg("Secret created")
	return nil
}

// UpdateSecretData updates specific keys in a module secret.
func (c *Client) UpdateSecretData(ctx context.Context, moduleID string, data map[string][]byte) error {
	secretName := fmt.Sprintf("polyon-module-%s", moduleID)
	
	secret, err := c.cs.CoreV1().Secrets(c.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}

	// Update the data
	for key, value := range data {
		secret.Data[key] = value
	}

	_, err = c.cs.CoreV1().Secrets(c.namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret %s: %w", secretName, err)
	}

	log.Info().Str("secret_name", secretName).Msg("Secret updated")
	return nil
}

// createModulePVCs creates PersistentVolumeClaims for the module.
func (c *Client) createModulePVCs(ctx context.Context, moduleID string, pvcSpecs []module.PVCSpec) error {
	for _, pvcSpec := range pvcSpecs {
		pvcName := fmt.Sprintf("polyon-%s-%s", moduleID, pvcSpec.Name)
		
		quantity, err := resource.ParseQuantity(pvcSpec.Size)
		if err != nil {
			return fmt.Errorf("invalid PVC size %s: %w", pvcSpec.Size, err)
		}

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: c.namespace,
				Labels: map[string]string{
					"app":             fmt.Sprintf("polyon-%s", moduleID),
					"polyon.io/module": moduleID,
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: quantity,
					},
				},
			},
		}

		_, err = c.cs.CoreV1().PersistentVolumeClaims(c.namespace).Create(ctx, pvc, metav1.CreateOptions{})
		if err != nil {
			if errors.IsAlreadyExists(err) {
				log.Info().Str("pvc_name", pvcName).Msg("PVC already exists, reusing")
			} else {
				return fmt.Errorf("failed to create PVC %s: %w", pvcName, err)
			}
		} else {
			log.Info().Str("pvc_name", pvcName).Str("size", pvcSpec.Size).Msg("PVC created")
		}
	}

	return nil
}

// createModuleService creates a K8s Service for the module.
func (c *Client) createModuleService(ctx context.Context, moduleID string, ports []module.PortSpec) error {
	serviceName := fmt.Sprintf("polyon-%s", moduleID)

	servicePorts := make([]corev1.ServicePort, 0, len(ports))
	for _, port := range ports {
		protocol := corev1.ProtocolTCP
		if port.Protocol != "" {
			protocol = corev1.Protocol(port.Protocol)
		}

		servicePorts = append(servicePorts, corev1.ServicePort{
			Name:       port.Name,
			Port:       int32(port.ContainerPort),
			TargetPort: intstr.FromInt(port.ContainerPort),
			Protocol:   protocol,
		})
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: c.namespace,
			Labels: map[string]string{
				"app":             fmt.Sprintf("polyon-%s", moduleID),
				"polyon.io/module": moduleID,
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: servicePorts,
			Selector: map[string]string{
				"app": fmt.Sprintf("polyon-%s", moduleID),
			},
		},
	}

	_, err := c.cs.CoreV1().Services(c.namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create service %s: %w", serviceName, err)
	}

	log.Info().Str("service_name", serviceName).Msg("Service created")
	return nil
}

// createModuleDeployment creates a K8s Deployment for the module.
func (c *Client) createModuleDeployment(ctx context.Context, moduleID string, resources module.ResourcesSpec) error {
	deploymentName := fmt.Sprintf("polyon-%s", moduleID)
	
	replicas := int32(1)
	if resources.Replicas > 0 {
		replicas = int32(resources.Replicas)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: c.namespace,
			Labels: map[string]string{
				"app":             fmt.Sprintf("polyon-%s", moduleID),
				"polyon.io/module": moduleID,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": fmt.Sprintf("polyon-%s", moduleID),
				},
			},
			Template: c.buildPodTemplate(moduleID, resources),
		},
	}

	_, err := c.cs.AppsV1().Deployments(c.namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create deployment %s: %w", deploymentName, err)
	}

	log.Info().Str("deployment_name", deploymentName).Msg("Deployment created")
	return nil
}

// createModuleStatefulSet creates a K8s StatefulSet for the module.
func (c *Client) createModuleStatefulSet(ctx context.Context, moduleID string, resources module.ResourcesSpec) error {
	statefulSetName := fmt.Sprintf("polyon-%s", moduleID)
	
	replicas := int32(1)
	if resources.Replicas > 0 {
		replicas = int32(resources.Replicas)
	}

	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName,
			Namespace: c.namespace,
			Labels: map[string]string{
				"app":             fmt.Sprintf("polyon-%s", moduleID),
				"polyon.io/module": moduleID,
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: fmt.Sprintf("polyon-%s", moduleID),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": fmt.Sprintf("polyon-%s", moduleID),
				},
			},
			Template: c.buildPodTemplate(moduleID, resources),
		},
	}

	_, err := c.cs.AppsV1().StatefulSets(c.namespace).Create(ctx, statefulSet, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create statefulset %s: %w", statefulSetName, err)
	}

	log.Info().Str("statefulset_name", statefulSetName).Msg("StatefulSet created")
	return nil
}

// buildPodTemplate builds a pod template for Deployment or StatefulSet.
func (c *Client) buildPodTemplate(moduleID string, resources module.ResourcesSpec) corev1.PodTemplateSpec {
	// Build environment variables
	envVars := make([]corev1.EnvVar, 0, len(resources.Env))
	for _, env := range resources.Env {
		envVar := corev1.EnvVar{
			Name: env.Name,
		}
		
		if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
			envVar.ValueFrom = &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: env.ValueFrom.SecretKeyRef.Name,
					},
					Key: env.ValueFrom.SecretKeyRef.Key,
				},
			}
		} else {
			envVar.Value = env.Value
		}
		
		envVars = append(envVars, envVar)
	}

	// Build container ports
	containerPorts := make([]corev1.ContainerPort, 0, len(resources.Ports))
	for _, port := range resources.Ports {
		protocol := corev1.ProtocolTCP
		if port.Protocol != "" {
			protocol = corev1.Protocol(port.Protocol)
		}

		containerPorts = append(containerPorts, corev1.ContainerPort{
			Name:          port.Name,
			ContainerPort: int32(port.ContainerPort),
			Protocol:      protocol,
		})
	}

	// Build volume mounts
	volumeMounts := make([]corev1.VolumeMount, 0, len(resources.PVC))
	for _, pvc := range resources.PVC {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      pvc.Name,
			MountPath: pvc.MountPath,
		})
	}

	// Build volumes
	volumes := make([]corev1.Volume, 0, len(resources.PVC))
	for _, pvc := range resources.PVC {
		volumes = append(volumes, corev1.Volume{
			Name: pvc.Name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: fmt.Sprintf("polyon-%s-%s", moduleID, pvc.Name),
				},
			},
		})
	}

	// Build resource requirements
	var resourceRequirements *corev1.ResourceRequirements
	if resources.Resources.Requests.CPU != "" || resources.Resources.Requests.Memory != "" ||
		resources.Resources.Limits.CPU != "" || resources.Resources.Limits.Memory != "" {
		resourceRequirements = &corev1.ResourceRequirements{}

		if resources.Resources.Requests.CPU != "" || resources.Resources.Requests.Memory != "" {
			resourceRequirements.Requests = corev1.ResourceList{}
			if resources.Resources.Requests.CPU != "" {
				resourceRequirements.Requests[corev1.ResourceCPU] = resource.MustParse(resources.Resources.Requests.CPU)
			}
			if resources.Resources.Requests.Memory != "" {
				resourceRequirements.Requests[corev1.ResourceMemory] = resource.MustParse(resources.Resources.Requests.Memory)
			}
		}

		if resources.Resources.Limits.CPU != "" || resources.Resources.Limits.Memory != "" {
			resourceRequirements.Limits = corev1.ResourceList{}
			if resources.Resources.Limits.CPU != "" {
				resourceRequirements.Limits[corev1.ResourceCPU] = resource.MustParse(resources.Resources.Limits.CPU)
			}
			if resources.Resources.Limits.Memory != "" {
				resourceRequirements.Limits[corev1.ResourceMemory] = resource.MustParse(resources.Resources.Limits.Memory)
			}
		}
	}

	// Build liveness and readiness probes
	var livenessProbe, readinessProbe *corev1.Probe
	if resources.Health.Path != "" {
		httpGet := &corev1.HTTPGetAction{
			Path: resources.Health.Path,
			Port: intstr.FromInt(resources.Health.Port),
		}

		initialDelay := int32(30)
		if resources.Health.InitialDelay > 0 {
			initialDelay = int32(resources.Health.InitialDelay)
		}

		period := int32(10)
		if resources.Health.Period > 0 {
			period = int32(resources.Health.Period)
		}

		livenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: httpGet,
			},
			InitialDelaySeconds: initialDelay,
			PeriodSeconds:       period,
		}

		readinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: httpGet,
			},
			InitialDelaySeconds: initialDelay / 2,
			PeriodSeconds:       period / 2,
		}
	}

	// Inject all secret keys as env vars via envFrom
	secretName := fmt.Sprintf("polyon-module-%s", moduleID)
	envFrom := []corev1.EnvFromSource{
		{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
			},
		},
	}

	container := corev1.Container{
		Name:            moduleID,
		Image:           resources.Image,
		Command:         resources.Command,
		Args:            resources.Args,
		Ports:           containerPorts,
		Env:             envVars,
		EnvFrom:         envFrom,
		VolumeMounts:    volumeMounts,
		LivenessProbe:   livenessProbe,
		ReadinessProbe:  readinessProbe,
	}

	if resourceRequirements != nil {
		container.Resources = *resourceRequirements
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app":             fmt.Sprintf("polyon-%s", moduleID),
				"polyon.io/module": moduleID,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{container},
			Volumes:    volumes,
		},
	}
}

// createModuleIngress creates a K8s Ingress for the module.
func (c *Client) createModuleIngress(ctx context.Context, moduleID string, ingressSpec module.IngressSpec) error {
	ingressName := fmt.Sprintf("polyon-%s", moduleID)
	serviceName := fmt.Sprintf("polyon-%s", moduleID)
	
	baseDomain := os.Getenv("POLYON_DOMAIN")
	if baseDomain == "" {
		baseDomain = "cmars.com"
	}
	baseDomain = strings.ToLower(baseDomain)

	// Determine host and path based on access mode
	var host, path string
	if ingressSpec.Subdomain != "" {
		// 서브도메인 방식: chat.cmars.com
		host = fmt.Sprintf("%s.%s", ingressSpec.Subdomain, baseDomain)
		path = "/"
	} else if ingressSpec.PathPrefix != "" {
		// URL 패턴 방식: portal.cmars.com/chat
		portalDomain := os.Getenv("POLYON_PORTAL_DOMAIN")
		if portalDomain == "" {
			portalDomain = fmt.Sprintf("portal.%s", baseDomain)
		}
		host = portalDomain
		path = ingressSpec.PathPrefix
	} else {
		return nil // no ingress needed
	}

	// Default annotations for Traefik (based on existing polyon ingresses)
	annotations := map[string]string{
		"traefik.ingress.kubernetes.io/router.entrypoints": "websecure",
	}

	// Add custom annotations
	for key, value := range ingressSpec.Annotations {
		annotations[key] = value
	}

	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ingressName,
			Namespace:   c.namespace,
			Annotations: annotations,
			Labels: map[string]string{
				"app":             fmt.Sprintf("polyon-%s", moduleID),
				"polyon.io/module": moduleID,
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("traefik"),
			TLS: []networkingv1.IngressTLS{
				{
					Hosts: []string{host},
					SecretName: "polyon-tls", // Reuse existing TLS secret
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{
												Number: int32(ingressSpec.Port),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := c.cs.NetworkingV1().Ingresses(c.namespace).Create(ctx, ingress, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ingress %s: %w", ingressName, err)
	}

	log.Info().Str("ingress_name", ingressName).Str("host", host).Msg("Ingress created")
	return nil
}

// stringPtr returns a pointer to a string value.
func stringPtr(s string) *string {
	return &s
}
// GetSecret returns a module secret's string data.
func (c *Client) GetSecret(ctx context.Context, secretName string) (map[string]string, error) {
	secret, err := c.cs.CoreV1().Secrets(c.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		result[k] = string(v)
	}
	return result, nil
}

// PatchSecret updates or adds string keys to an existing secret.
func (c *Client) PatchSecret(ctx context.Context, secretName string, data map[string]string) error {
	secret, err := c.cs.CoreV1().Secrets(c.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	for k, v := range data {
		secret.Data[k] = []byte(v)
	}
	_, err = c.cs.CoreV1().Secrets(c.namespace).Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

// ExecInPod runs a command in a pod (best-effort, no output capture).
// IsServiceAvailable checks if a Foundation service is running in K8s.
// Looks for service named "polyon-{id}" with at least one ready endpoint.
func (c *Client) IsServiceAvailable(ctx context.Context, serviceID string) bool {
	names := []string{
		fmt.Sprintf("polyon-%s", serviceID),
		serviceID,
	}
	for _, name := range names {
		_, err := c.cs.CoreV1().Services(c.namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			return true
		}
	}
	return false
}

func (c *Client) ExecInPod(ctx context.Context, podName string, cmd []string) error {
	// Use kubectl exec via os/exec for simplicity
	args := append([]string{"exec", "-n", c.namespace, podName, "--"}, cmd...)
	out, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
	if err != nil {
		log.Warn().Str("pod", podName).Str("output", string(out)).Err(err).Msg("ExecInPod failed")
	}
	return err
}
