package workloads

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/labels"

	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"
	"code.cloudfoundry.org/korifi/controllers/controllers/shared"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

func createOrPatchNamespace(ctx context.Context, client client.Client, orgOrSpace client.Object, labels map[string]string, annotations map[string]string) error {
	log := logr.FromContextOrDiscard(ctx).WithName("createOrPatchNamespace")

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: orgOrSpace.GetName(),
		},
	}

	result, err := controllerutil.CreateOrPatch(ctx, client, namespace, func() error {
		updateMap(&namespace.Annotations, annotations)
		updateMap(&namespace.Labels, labels)
		return nil
	})
	if err != nil {
		return err
	}

	log.V(1).Info("Namespace reconciled", "operation", result)
	return nil
}

func updateMap(dest *map[string]string, values map[string]string) {
	if *dest == nil {
		*dest = make(map[string]string)
	}

	for key, value := range values {
		(*dest)[key] = value
	}
}

func propagateSecrets(ctx context.Context, client client.Client, orgOrSpace client.Object, secretNames []string) error {
	if len(secretNames) == 0 {
		// we are operating in service account role association mode for registry permissions.
		// only tested implicity in EKS e2es
		return nil
	}

	log := logr.FromContextOrDiscard(ctx).WithName("propagateSecret").
		WithValues("parentNamespace", orgOrSpace.GetNamespace(), "targetNamespace", orgOrSpace.GetName())

	for _, secretName := range secretNames {
		looplog := log.WithValues("secretName", secretName)

		secret := new(corev1.Secret)
		err := client.Get(ctx, types.NamespacedName{Namespace: orgOrSpace.GetNamespace(), Name: secretName}, secret)
		if err != nil {
			return fmt.Errorf("error fetching secret %q from namespace %q: %w", secretName, orgOrSpace.GetNamespace(), err)
		}

		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secret.Name,
				Namespace: orgOrSpace.GetName(),
			},
		}

		result, err := controllerutil.CreateOrPatch(ctx, client, newSecret, func() error {
			newSecret.Annotations = shared.RemovePackageManagerKeys(secret.Annotations, looplog)
			newSecret.Labels = shared.RemovePackageManagerKeys(secret.Labels, looplog)
			newSecret.Immutable = secret.Immutable
			newSecret.Data = secret.Data
			newSecret.StringData = secret.StringData
			newSecret.Type = secret.Type
			return nil
		})
		if err != nil {
			return fmt.Errorf("error creating/patching secret: %w", err)
		}

		looplog.V(1).Info("Secret propagated", "operation", result)
	}

	log.V(1).Info("All secrets propagated")

	return nil
}

func reconcileRoleBindings(ctx context.Context, kClient client.Client, orgOrSpace client.Object) error {
	log := logr.FromContextOrDiscard(ctx).WithName("propagateRolebindings").
		WithValues("parentNamespace", orgOrSpace.GetNamespace(), "targetNamespace", orgOrSpace.GetName())

	roleBindings := new(rbacv1.RoleBindingList)
	err := kClient.List(ctx, roleBindings, client.InNamespace(orgOrSpace.GetNamespace()))
	if err != nil {
		log.Info("error listing role-bindings from the parent namespace", "reason", err)
		return err
	}

	var result controllerutil.OperationResult
	parentRoleBindingMap := make(map[string]struct{})
	for _, binding := range roleBindings.Items {
		if binding.Annotations[korifiv1alpha1.PropagateRoleBindingAnnotation] == "true" {
			loopLog := log.WithValues("roleBindingName", binding.Name)

			parentRoleBindingMap[binding.Name] = struct{}{}

			newRoleBinding := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      binding.Name,
					Namespace: orgOrSpace.GetName(),
				},
			}

			result, err = controllerutil.CreateOrPatch(ctx, kClient, newRoleBinding, func() error {
				newRoleBinding.Annotations = shared.RemovePackageManagerKeys(binding.Annotations, log)
				newRoleBinding.Labels = shared.RemovePackageManagerKeys(binding.Labels, log)
				if newRoleBinding.Labels == nil {
					newRoleBinding.Labels = map[string]string{}
				}
				newRoleBinding.Labels[korifiv1alpha1.PropagatedFromLabel] = orgOrSpace.GetNamespace()
				newRoleBinding.Subjects = binding.Subjects
				newRoleBinding.RoleRef = binding.RoleRef
				return nil
			})
			if err != nil {
				loopLog.Info("error propagating role-binding", "reason", err)
				return err
			}

			loopLog.V(1).Info("Role Binding propagated", "operation", result)
		}
	}

	propagatedRoleBindings := new(rbacv1.RoleBindingList)
	labelSelector, err := labels.ValidatedSelectorFromSet(map[string]string{
		korifiv1alpha1.PropagatedFromLabel: orgOrSpace.GetNamespace(),
	})
	if err != nil {
		log.Info("failed to create label selector", "reason", err)
		return err
	}

	err = kClient.List(ctx, propagatedRoleBindings, &client.ListOptions{Namespace: orgOrSpace.GetName(), LabelSelector: labelSelector})
	if err != nil {
		log.Info("error listing role-bindings from parent namespace", "reason", err)
		return err
	}

	for index := range propagatedRoleBindings.Items {
		propagatedRoleBinding := propagatedRoleBindings.Items[index]
		if propagatedRoleBinding.Annotations[korifiv1alpha1.PropagateDeletionAnnotation] == "false" {
			continue
		}

		if _, found := parentRoleBindingMap[propagatedRoleBinding.Name]; !found {
			err = kClient.Delete(ctx, &propagatedRoleBinding)
			if err != nil {
				log.Info("deleting role binding from target namespace failed", "roleBindingName", propagatedRoleBinding.Name, "reason", err)
				return err
			}
		}
	}

	return nil
}

func getNamespace(ctx context.Context, client client.Client, namespaceName string) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("namespace", namespaceName)

	namespace := new(corev1.Namespace)
	err := client.Get(ctx, types.NamespacedName{Name: namespaceName}, namespace)
	if err != nil {
		log.Info("failed to get namespace", "reason", err)
		return err
	}

	return nil
}

func logAndSetReadyStatus(err error, log logr.Logger, conditions *[]metav1.Condition, reason string, generation int64) (ctrl.Result, error) {
	log.Info(err.Error())

	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               shared.StatusConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: generation,
	})

	return ctrl.Result{}, err
}
