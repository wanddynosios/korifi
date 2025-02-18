package workloads_test

import (
	"context"
	"errors"

	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"
	. "code.cloudfoundry.org/korifi/controllers/controllers/workloads/testutils"
	"code.cloudfoundry.org/korifi/tools"
	"code.cloudfoundry.org/korifi/tools/k8s"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("CFBuildReconciler Integration Tests", func() {
	const (
		stagingConditionType                = "Staging"
		succeededConditionType              = "Succeeded"
		wellFormedRegistryCredentialsSecret = "image-registry-credentials"
	)

	var (
		cfSpace           *korifiv1alpha1.CFSpace
		cfAppGUID         string
		cfPackageGUID     string
		cfBuildGUID       string
		desiredCFApp      *korifiv1alpha1.CFApp
		desiredCFPackage  *korifiv1alpha1.CFPackage
		desiredCFBuild    *korifiv1alpha1.CFBuild
		desiredBuildpacks []string
	)

	eventuallyBuildWorkloadShould := func(assertion func(*korifiv1alpha1.BuildWorkload, Gomega)) {
		GinkgoHelper()

		Eventually(func(g Gomega) {
			workload := new(korifiv1alpha1.BuildWorkload)
			lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
			g.Expect(adminClient.Get(context.Background(), lookupKey, workload)).To(Succeed())
			assertion(workload, g)
		}).Should(Succeed())
	}

	BeforeEach(func() {
		cfSpace = createSpace(cfOrg)
		cfAppGUID = PrefixedGUID("cf-app")
		cfPackageGUID = PrefixedGUID("cf-package")

		beforeCtx := context.Background()

		desiredCFApp = BuildCFAppCRObject(cfAppGUID, cfSpace.Status.GUID)
		Expect(adminClient.Create(beforeCtx, desiredCFApp)).To(Succeed())

		Eventually(func(g Gomega) {
			actualCFApp := &korifiv1alpha1.CFApp{}
			g.Expect(adminClient.Get(beforeCtx, types.NamespacedName{Name: cfAppGUID, Namespace: cfSpace.Status.GUID}, actualCFApp)).To(Succeed())
			g.Expect(actualCFApp.Status.VCAPServicesSecretName).NotTo(BeEmpty())
		}).Should(Succeed())

		envVarSecret := BuildCFAppEnvVarsSecret(desiredCFApp.Name, cfSpace.Status.GUID, map[string]string{
			"a_key": "a-val",
			"b_key": "b-val",
		})
		Expect(adminClient.Create(context.Background(), envVarSecret)).To(Succeed())

		dockerRegistrySecret := BuildDockerRegistrySecret(wellFormedRegistryCredentialsSecret, cfSpace.Status.GUID)
		Expect(adminClient.Create(beforeCtx, dockerRegistrySecret)).To(Succeed())

		registryServiceAccountName := "kpack-service-account"
		registryServiceAccount := BuildServiceAccount(registryServiceAccountName, cfSpace.Status.GUID, wellFormedRegistryCredentialsSecret)
		Expect(adminClient.Create(beforeCtx, registryServiceAccount)).To(Succeed())

		desiredBuildpacks = []string{"first-buildpack", "second-buildpack"}
	})

	When("CFBuild status conditions are missing or unknown", func() {
		var cleanCallCount int

		BeforeEach(func() {
			cleanCallCount = buildCleaner.CleanCallCount()
			desiredCFPackage = BuildCFPackageCRObject(cfPackageGUID, cfSpace.Status.GUID, cfAppGUID, "ref")
			Expect(adminClient.Create(ctx, desiredCFPackage)).To(Succeed())

			kpackSecret := BuildDockerRegistrySecret("source-registry-image-pull-secret", cfSpace.Status.GUID)
			Expect(adminClient.Create(ctx, kpackSecret)).To(Succeed())
		})

		JustBeforeEach(func() {
			cfBuildGUID = PrefixedGUID("cf-build")
			desiredCFBuild = BuildCFBuildObject(cfBuildGUID, cfSpace.Status.GUID, cfPackageGUID, cfAppGUID)
			desiredCFBuild.Spec.Lifecycle.Data.Buildpacks = desiredBuildpacks
			Expect(adminClient.Create(context.Background(), desiredCFBuild)).To(Succeed())
		})

		It("cleans up older builds and droplets", func() {
			Eventually(func(g Gomega) {
				for i := cleanCallCount; i < buildCleaner.CleanCallCount(); i++ {
					_, app := buildCleaner.CleanArgsForCall(i)
					if app.Name == cfAppGUID && app.Namespace == cfSpace.Status.GUID {
						return
					}
				}
				g.Expect(errors.New("Clean() has not been invoked with expected args")).NotTo(HaveOccurred())
			}).Should(Succeed())
		})

		It("reconciles to set the owner reference on the CFBuild", func() {
			Eventually(func(g Gomega) {
				var createdCFBuild korifiv1alpha1.CFBuild
				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				g.Expect(adminClient.Get(context.Background(), lookupKey, &createdCFBuild)).To(Succeed())
				g.Expect(createdCFBuild.GetOwnerReferences()).To(ConsistOf(
					metav1.OwnerReference{
						APIVersion:         korifiv1alpha1.GroupVersion.Identifier(),
						Kind:               "CFApp",
						Name:               desiredCFApp.Name,
						UID:                desiredCFApp.UID,
						Controller:         tools.PtrTo(true),
						BlockOwnerDeletion: tools.PtrTo(true),
					},
				))
			}).Should(Succeed())
		})

		It("sets the ObservedGeneration status field", func() {
			Eventually(func(g Gomega) {
				var createdCFBuild korifiv1alpha1.CFBuild
				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				g.Expect(adminClient.Get(context.Background(), lookupKey, &createdCFBuild)).To(Succeed())
				g.Expect(createdCFBuild.Status.ObservedGeneration).To(Equal(createdCFBuild.Generation))
			}).Should(Succeed())
		})

		It("creates a BuildWorkload with the buildRef, source, env, and buildpacks set", func() {
			createdCFApp := &korifiv1alpha1.CFApp{}
			Expect(adminClient.Get(context.Background(), types.NamespacedName{Name: cfAppGUID, Namespace: cfSpace.Status.GUID}, createdCFApp)).To(Succeed())

			eventuallyBuildWorkloadShould(func(workload *korifiv1alpha1.BuildWorkload, g Gomega) {
				g.Expect(workload.Spec.BuildRef.Name).To(Equal(cfBuildGUID))
				g.Expect(workload.Spec.Source).To(Equal(desiredCFPackage.Spec.Source))
				g.Expect(workload.Spec.Env).To(ConsistOf(
					MatchFields(IgnoreExtras, Fields{
						"Name": Equal("a_key"),
						"ValueFrom": PointTo(MatchFields(IgnoreExtras, Fields{
							"SecretKeyRef": PointTo(MatchFields(IgnoreExtras, Fields{
								"LocalObjectReference": MatchFields(IgnoreExtras, Fields{
									"Name": Equal(createdCFApp.Spec.EnvSecretName),
								}),
								"Key": Equal("a_key"),
							})),
						})),
					}),
					MatchFields(IgnoreExtras, Fields{
						"Name": Equal("b_key"),
						"ValueFrom": PointTo(MatchFields(IgnoreExtras, Fields{
							"SecretKeyRef": PointTo(MatchFields(IgnoreExtras, Fields{
								"LocalObjectReference": MatchFields(IgnoreExtras, Fields{
									"Name": Equal(createdCFApp.Spec.EnvSecretName),
								}),
								"Key": Equal("b_key"),
							})),
						})),
					}),
					MatchFields(IgnoreExtras, Fields{
						"Name": Equal("VCAP_SERVICES"),
						"ValueFrom": PointTo(MatchFields(IgnoreExtras, Fields{
							"SecretKeyRef": PointTo(MatchFields(IgnoreExtras, Fields{
								"LocalObjectReference": MatchFields(IgnoreExtras, Fields{
									"Name": Equal(createdCFApp.Status.VCAPServicesSecretName),
								}),
								"Key": Equal("VCAP_SERVICES"),
							})),
						})),
					}),
					MatchFields(IgnoreExtras, Fields{
						"Name": Equal("VCAP_APPLICATION"),
						"ValueFrom": PointTo(MatchFields(IgnoreExtras, Fields{
							"SecretKeyRef": PointTo(MatchFields(IgnoreExtras, Fields{
								"LocalObjectReference": MatchFields(IgnoreExtras, Fields{
									"Name": Equal(createdCFApp.Status.VCAPApplicationSecretName),
								}),
								"Key": Equal("VCAP_APPLICATION"),
							})),
						})),
					}),
				))
				g.Expect(workload.Spec.Buildpacks).To(Equal(desiredBuildpacks))
			})
		})

		When("BuildWorkload with CFBuild GUID doesn't exist", func() {
			It("creates a BuildWorkload owned by the CFBuild", func() {
				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				Eventually(func(g Gomega) {
					createdWorkload := new(korifiv1alpha1.BuildWorkload)
					g.Expect(adminClient.Get(context.Background(), lookupKey, createdWorkload)).To(Succeed())
					g.Expect(createdWorkload.GetOwnerReferences()).To(ConsistOf(metav1.OwnerReference{
						UID:                desiredCFBuild.UID,
						Kind:               "CFBuild",
						APIVersion:         "korifi.cloudfoundry.org/v1alpha1",
						Name:               desiredCFBuild.Name,
						Controller:         tools.PtrTo(true),
						BlockOwnerDeletion: tools.PtrTo(true),
					}))
				}).Should(Succeed())
			})

			It("sets the status conditions on CFBuild", func() {
				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				Eventually(func(g Gomega) {
					createdCFBuild := new(korifiv1alpha1.CFBuild)
					g.Expect(adminClient.Get(context.Background(), lookupKey, createdCFBuild)).To(Succeed())

					stagingCondition := meta.FindStatusCondition(createdCFBuild.Status.Conditions, stagingConditionType)
					g.Expect(stagingCondition).NotTo(BeNil())
					g.Expect(stagingCondition.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(stagingCondition.Reason).To(Equal("BuildRunning"))
					g.Expect(stagingCondition.ObservedGeneration).To(Equal(createdCFBuild.Generation))

					succeededCondition := meta.FindStatusCondition(createdCFBuild.Status.Conditions, succeededConditionType)
					g.Expect(succeededCondition).NotTo(BeNil())
					g.Expect(succeededCondition.Status).To(Equal(metav1.ConditionUnknown))
					g.Expect(succeededCondition.ObservedGeneration).To(Equal(createdCFBuild.Generation))
				}).Should(Succeed())
			})
		})

		When("the referenced app has a ServiceBinding and Secret", func() {
			var (
				secret1          *corev1.Secret
				secret2          *corev1.Secret
				serviceInstance1 *korifiv1alpha1.CFServiceInstance
				serviceInstance2 *korifiv1alpha1.CFServiceInstance
				serviceBinding1  *korifiv1alpha1.CFServiceBinding
				serviceBinding2  *korifiv1alpha1.CFServiceBinding
			)

			BeforeEach(func() {
				secret1Data := map[string]string{
					"foo": "bar",
				}
				secret1 = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret1",
						Namespace: cfSpace.Status.GUID,
					},
					StringData: secret1Data,
				}
				Expect(
					adminClient.Create(ctx, secret1),
				).To(Succeed())

				serviceInstance1 = &korifiv1alpha1.CFServiceInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-instance-1-guid",
						Namespace: cfSpace.Status.GUID,
					},
					Spec: korifiv1alpha1.CFServiceInstanceSpec{
						DisplayName: "service-instance-1-name",
						SecretName:  secret1.Name,
						Type:        "user-provided",
						Tags: []string{
							"tag1",
							"tag2",
						},
					},
				}
				Expect(
					adminClient.Create(ctx, serviceInstance1),
				).To(Succeed())

				serviceBinding1Name := "service-binding-1-name"
				serviceBinding1 = &korifiv1alpha1.CFServiceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-binding-1-guid",
						Namespace: cfSpace.Status.GUID,
						Labels: map[string]string{
							korifiv1alpha1.CFAppGUIDLabelKey: desiredCFApp.Name,
						},
					},
					Spec: korifiv1alpha1.CFServiceBindingSpec{
						DisplayName: &serviceBinding1Name,
						Service: corev1.ObjectReference{
							Kind:       "ServiceInstance",
							Name:       serviceInstance1.Name,
							APIVersion: "korifi.cloudfoundry.org/v1alpha1",
						},
						AppRef: corev1.LocalObjectReference{
							Name: desiredCFApp.Name,
						},
					},
				}
				Expect(
					adminClient.Create(ctx, serviceBinding1),
				).To(Succeed())

				secret2Data := map[string]string{
					"key1": "value1",
					"key2": "value2",
				}
				secret2 = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret2",
						Namespace: cfSpace.Status.GUID,
					},
					StringData: secret2Data,
				}
				Expect(
					adminClient.Create(ctx, secret2),
				).To(Succeed())

				serviceInstance2 = &korifiv1alpha1.CFServiceInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-instance-2-guid",
						Namespace: cfSpace.Status.GUID,
					},
					Spec: korifiv1alpha1.CFServiceInstanceSpec{
						DisplayName: "service-instance-2-name",
						SecretName:  secret2.Name,
						Type:        "user-provided",
						Tags:        []string{},
					},
				}
				Expect(
					adminClient.Create(ctx, serviceInstance2),
				).To(Succeed())

				serviceBinding2Name := "service-binding-2-name"
				serviceBinding2 = &korifiv1alpha1.CFServiceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-binding-2-guid",
						Namespace: cfSpace.Status.GUID,
						Labels: map[string]string{
							korifiv1alpha1.CFAppGUIDLabelKey: desiredCFApp.Name,
						},
					},
					Spec: korifiv1alpha1.CFServiceBindingSpec{
						DisplayName: &serviceBinding2Name,
						Service: corev1.ObjectReference{
							Kind:       "ServiceInstance",
							Name:       serviceInstance2.Name,
							APIVersion: "korifi.cloudfoundry.org/v1alpha1",
						},
						AppRef: corev1.LocalObjectReference{
							Name: desiredCFApp.Name,
						},
					},
				}
				Expect(
					adminClient.Create(ctx, serviceBinding2),
				).To(Succeed())

				createdServiceBinding1 := serviceBinding1.DeepCopy()
				createdServiceBinding1.Status.Binding.Name = secret1.Name
				meta.SetStatusCondition(&createdServiceBinding1.Status.Conditions, metav1.Condition{
					Type:    "BindingSecretAvailable",
					Status:  metav1.ConditionTrue,
					Reason:  "SecretFound",
					Message: "",
				})
				Expect(adminClient.Status().Patch(ctx, createdServiceBinding1, client.MergeFrom(serviceBinding1))).To(Succeed())

				createdServiceBinding2 := serviceBinding2.DeepCopy()
				createdServiceBinding2.Status.Binding.Name = secret2.Name
				meta.SetStatusCondition(&createdServiceBinding2.Status.Conditions, metav1.Condition{
					Type:    "BindingSecretAvailable",
					Status:  metav1.ConditionTrue,
					Reason:  "SecretFound",
					Message: "",
				})
				Expect(adminClient.Status().Patch(ctx, createdServiceBinding2, client.MergeFrom(serviceBinding2))).To(Succeed())
			})

			It("creates a BuildWorkload with the underlying secret mapped onto it", func() {
				eventuallyBuildWorkloadShould(func(workload *korifiv1alpha1.BuildWorkload, g Gomega) {
					g.Expect(workload.Spec.Services).To(ConsistOf(
						MatchFields(IgnoreExtras, Fields{
							"Name":       Equal(secret1.Name),
							"Kind":       Equal("Secret"),
							"APIVersion": Equal("v1"),
						}),
						MatchFields(IgnoreExtras, Fields{
							"Name":       Equal(secret2.Name),
							"Kind":       Equal("Secret"),
							"APIVersion": Equal("v1"),
						}),
					))
				})
			})

			It("sets the VCAP_SERVICES env var in the image", func() {
				createdCFApp := &korifiv1alpha1.CFApp{}
				Expect(adminClient.Get(context.Background(), types.NamespacedName{Name: cfAppGUID, Namespace: cfSpace.Status.GUID}, createdCFApp)).To(Succeed())

				eventuallyBuildWorkloadShould(func(workload *korifiv1alpha1.BuildWorkload, g Gomega) {
					g.Expect(workload.Spec.Env).To(ContainElements(
						MatchFields(IgnoreExtras, Fields{
							"Name": Equal("VCAP_SERVICES"),
							"ValueFrom": PointTo(MatchFields(IgnoreExtras, Fields{
								"SecretKeyRef": PointTo(MatchFields(IgnoreExtras, Fields{
									"Key": Equal("VCAP_SERVICES"),
									"LocalObjectReference": MatchFields(IgnoreExtras, Fields{
										"Name": Equal(createdCFApp.Status.VCAPServicesSecretName),
									}),
								})),
							})),
						}),
					))
				})
			})
		})

		When("a BuildWorkload with CFBuild GUID already exists", func() {
			var (
				newCFBuildGUID        string
				existingBuildWorkload *korifiv1alpha1.BuildWorkload
				newCFBuild            *korifiv1alpha1.CFBuild
			)

			BeforeEach(func() {
				newCFBuildGUID = PrefixedGUID("new-cf-build")
				existingBuildWorkload = &korifiv1alpha1.BuildWorkload{
					ObjectMeta: metav1.ObjectMeta{
						Name:      newCFBuildGUID,
						Namespace: cfSpace.Status.GUID,
					},
					Spec: korifiv1alpha1.BuildWorkloadSpec{
						Source: korifiv1alpha1.PackageSource{
							Registry: korifiv1alpha1.Registry{
								Image:            "not-an-image",
								ImagePullSecrets: nil,
							},
						},
					},
				}
				newCFBuild = BuildCFBuildObject(newCFBuildGUID, cfSpace.Status.GUID, cfPackageGUID, cfAppGUID)

				Expect(adminClient.Create(ctx, existingBuildWorkload)).To(Succeed())
				Expect(adminClient.Create(ctx, newCFBuild)).To(Succeed())
			})

			It("sets the status conditions on CFBuild", func() {
				lookupKey := types.NamespacedName{Name: newCFBuildGUID, Namespace: cfSpace.Status.GUID}
				Eventually(func(g Gomega) {
					createdCFBuild := new(korifiv1alpha1.CFBuild)
					g.Expect(adminClient.Get(context.Background(), lookupKey, createdCFBuild)).To(Succeed())

					stagingCondition := meta.FindStatusCondition(createdCFBuild.Status.Conditions, stagingConditionType)
					g.Expect(stagingCondition).NotTo(BeNil())
					g.Expect(stagingCondition.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(stagingCondition.Reason).To(Equal("BuildRunning"))
					g.Expect(stagingCondition.ObservedGeneration).To(Equal(createdCFBuild.Generation))

					succeededCondition := meta.FindStatusCondition(createdCFBuild.Status.Conditions, succeededConditionType)
					g.Expect(succeededCondition).NotTo(BeNil())
					g.Expect(succeededCondition.Status).To(Equal(metav1.ConditionUnknown))
					g.Expect(succeededCondition.ObservedGeneration).To(Equal(createdCFBuild.Generation))
				}).Should(Succeed())
			})
		})

		It("writes a log message", func() {
			Eventually(logOutput).Should(gbytes.Say("set observed generation"))
		})
	})

	When("CFBuild status conditions Staging=True and others are unknown", func() {
		BeforeEach(func() {
			desiredCFPackage = BuildCFPackageCRObject(cfPackageGUID, cfSpace.Status.GUID, cfAppGUID, "ref")
			desiredCFPackage.Spec.Source.Registry.ImagePullSecrets = []corev1.LocalObjectReference{{Name: wellFormedRegistryCredentialsSecret}}
			Expect(adminClient.Create(context.Background(), desiredCFPackage)).To(Succeed())

			cfBuildGUID = PrefixedGUID("cf-build")
			desiredCFBuild = BuildCFBuildObject(cfBuildGUID, cfSpace.Status.GUID, cfPackageGUID, cfAppGUID)
			Expect(adminClient.Create(context.Background(), desiredCFBuild)).To(Succeed())
		})

		When("the BuildWorkload failed", func() {
			BeforeEach(func() {
				testCtx := context.Background()
				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				Eventually(func(g Gomega) {
					workload := new(korifiv1alpha1.BuildWorkload)
					g.Expect(adminClient.Get(testCtx, lookupKey, workload)).To(Succeed())
					g.Expect(k8s.Patch(testCtx, adminClient, workload, func() {
						setBuildWorkloadStatus(workload, succeededConditionType, metav1.ConditionFalse)
					})).To(Succeed())
				}).Should(Succeed())
			})

			It("sets the CFBuild status condition Succeeded = False", func() {
				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				createdCFBuild := new(korifiv1alpha1.CFBuild)
				Eventually(func(g Gomega) {
					g.Expect(adminClient.Get(context.Background(), lookupKey, createdCFBuild)).To(Succeed())

					stagingStatusCondition := meta.FindStatusCondition(createdCFBuild.Status.Conditions, stagingConditionType)
					g.Expect(stagingStatusCondition).NotTo(BeNil())
					g.Expect(stagingStatusCondition.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(stagingStatusCondition.Reason).To(Equal("BuildNotRunning"))
					g.Expect(stagingStatusCondition.ObservedGeneration).To(Equal(createdCFBuild.Generation))

					succeededStatusCondition := meta.FindStatusCondition(createdCFBuild.Status.Conditions, succeededConditionType)
					g.Expect(succeededStatusCondition).NotTo(BeNil())
					g.Expect(succeededStatusCondition.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(succeededStatusCondition.Reason).To(Equal("BuildFailed"))
					g.Expect(succeededStatusCondition.ObservedGeneration).To(Equal(createdCFBuild.Generation))
				}).Should(Succeed())
			})
		})

		When("the BuildWorkload finished successfully", func() {
			const (
				buildImageRef       = "some-org/my-image@sha256:some-sha"
				imagePullSecretName = "image-pull-s3cr37"
				buildStack          = "cflinuxfs3"
			)

			var (
				returnedProcessTypes []korifiv1alpha1.ProcessType
				returnedPorts        []int32
			)

			BeforeEach(func() {
				returnedPorts = []int32{42}
				returnedProcessTypes = []korifiv1alpha1.ProcessType{
					{
						Type:    "web",
						Command: "run-stuff",
					},
				}

				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				Eventually(func(g Gomega) {
					workload := new(korifiv1alpha1.BuildWorkload)
					g.Expect(adminClient.Get(ctx, lookupKey, workload)).To(Succeed())
					g.Expect(k8s.Patch(ctx, adminClient, workload, func() {
						setBuildWorkloadStatus(workload, succeededConditionType, "True")
						workload.Status.Droplet = &korifiv1alpha1.BuildDropletStatus{
							Registry: korifiv1alpha1.Registry{
								Image:            buildImageRef,
								ImagePullSecrets: []corev1.LocalObjectReference{{Name: imagePullSecretName}},
							},
							Stack:        buildStack,
							Ports:        returnedPorts,
							ProcessTypes: returnedProcessTypes,
						}
					})).To(Succeed())
				}).Should(Succeed())
			})

			It("sets the CFBuild status condition Succeeded = True", func() {
				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				createdCFBuild := new(korifiv1alpha1.CFBuild)

				Eventually(func(g Gomega) {
					g.Expect(adminClient.Get(context.Background(), lookupKey, createdCFBuild)).To(Succeed())
					stagingStatusCondition := meta.FindStatusCondition(createdCFBuild.Status.Conditions, stagingConditionType)
					g.Expect(stagingStatusCondition).NotTo(BeNil())
					g.Expect(stagingStatusCondition.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(stagingStatusCondition.Reason).To(Equal("BuildNotRunning"))
					g.Expect(stagingStatusCondition.ObservedGeneration).To(Equal(createdCFBuild.Generation))

					succeededStatusCondition := meta.FindStatusCondition(createdCFBuild.Status.Conditions, succeededConditionType)
					g.Expect(succeededStatusCondition).NotTo(BeNil())
					g.Expect(succeededStatusCondition.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(succeededStatusCondition.Reason).To(Equal("BuildSucceeded"))
					g.Expect(succeededStatusCondition.ObservedGeneration).To(Equal(createdCFBuild.Generation))
				}).Should(Succeed())
			})

			It("sets CFBuild.status.droplet", func() {
				lookupKey := types.NamespacedName{Name: cfBuildGUID, Namespace: cfSpace.Status.GUID}
				Eventually(func(g Gomega) {
					createdCFBuild := new(korifiv1alpha1.CFBuild)
					g.Expect(adminClient.Get(context.Background(), lookupKey, createdCFBuild)).To(Succeed())
					g.Expect(createdCFBuild.Status.Droplet).NotTo(BeNil())
					g.Expect(createdCFBuild.Status.Droplet.Registry.Image).To(Equal(buildImageRef))
					g.Expect(createdCFBuild.Status.Droplet.Registry.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{Name: imagePullSecretName}))
					g.Expect(createdCFBuild.Status.Droplet.Stack).To(Equal(buildStack))
					g.Expect(createdCFBuild.Status.Droplet.ProcessTypes).To(Equal(returnedProcessTypes))
					g.Expect(createdCFBuild.Status.Droplet.Ports).To(Equal(returnedPorts))
				}).Should(Succeed())
			})
		})
	})
})

func setBuildWorkloadStatus(workload *korifiv1alpha1.BuildWorkload, conditionType string, conditionStatus metav1.ConditionStatus) {
	meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
		Type:   conditionType,
		Status: conditionStatus,
		Reason: "shrug",
	})
}
