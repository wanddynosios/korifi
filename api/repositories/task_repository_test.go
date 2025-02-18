package repositories_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"code.cloudfoundry.org/korifi/api/authorization"
	apierrors "code.cloudfoundry.org/korifi/api/errors"
	"code.cloudfoundry.org/korifi/api/repositories"
	"code.cloudfoundry.org/korifi/api/repositories/conditions"
	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"
	"code.cloudfoundry.org/korifi/tests/matchers"
	"code.cloudfoundry.org/korifi/tools/k8s"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("TaskRepository", func() {
	var (
		taskRepo          *repositories.TaskRepo
		org               *korifiv1alpha1.CFOrg
		space             *korifiv1alpha1.CFSpace
		cfApp             *korifiv1alpha1.CFApp
		simTaskController func(*korifiv1alpha1.CFTask)
		controllerSync    *sync.WaitGroup
	)

	setStatusAndUpdate := func(task *korifiv1alpha1.CFTask, conditionTypes ...string) {
		GinkgoHelper()

		for _, cond := range conditionTypes {
			meta.SetStatusCondition(&(task.Status.Conditions), metav1.Condition{
				Type:    cond,
				Status:  metav1.ConditionTrue,
				Reason:  "foo",
				Message: "bar",
			})
		}

		Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
	}

	defaultStatusValues := func(task *korifiv1alpha1.CFTask, seqId int64, dropletId string) *korifiv1alpha1.CFTask {
		task.Status.SequenceID = seqId
		task.Status.MemoryMB = 256
		task.Status.DiskQuotaMB = 128
		task.Status.DropletRef.Name = dropletId

		return task
	}

	BeforeEach(func() {
		taskRepo = repositories.NewTaskRepo(userClientFactory, namespaceRetriever, nsPerms, conditions.NewConditionAwaiter[*korifiv1alpha1.CFTask, korifiv1alpha1.CFTaskList](2*time.Second))

		org = createOrgWithCleanup(ctx, prefixedGUID("org"))
		space = createSpaceWithCleanup(ctx, org.Name, prefixedGUID("space"))

		cfApp = createApp(space.Name)

		simTaskController = func(cft *korifiv1alpha1.CFTask) {}
	})

	JustBeforeEach(func() {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, 2*time.Second)
		DeferCleanup(func() {
			cancel()
		})

		tasksWatch, err := k8sClient.Watch(
			ctxWithTimeout,
			&korifiv1alpha1.CFTaskList{},
			client.InNamespace(space.Name),
		)
		Expect(err).NotTo(HaveOccurred())

		watchChan := tasksWatch.ResultChan()

		controllerSync = &sync.WaitGroup{}
		controllerSync.Add(1)

		go func() {
			defer GinkgoRecover()
			defer controllerSync.Done()

			for e := range watchChan {
				cft, ok := e.Object.(*korifiv1alpha1.CFTask)
				if !ok {
					time.Sleep(100 * time.Millisecond)
					continue
				}

				simTaskController(cft)
			}
		}()
	})

	AfterEach(func() {
		controllerSync.Wait()
	})

	Describe("CreateTask", func() {
		var (
			createMessage repositories.CreateTaskMessage
			taskRecord    repositories.TaskRecord
			createErr     error
		)

		BeforeEach(func() {
			simTaskController = func(cft *korifiv1alpha1.CFTask) {
				setStatusAndUpdate(
					defaultStatusValues(cft, 6, cfApp.Spec.CurrentDropletRef.Name),
					korifiv1alpha1.TaskInitializedConditionType,
				)
			}
			createMessage = repositories.CreateTaskMessage{
				Command:   "echo 'hello world'",
				SpaceGUID: space.Name,
				AppGUID:   cfApp.Name,
				Metadata: repositories.Metadata{
					Labels:      map[string]string{"color": "blue"},
					Annotations: map[string]string{"extra-bugs": "true"},
				},
			}
		})

		JustBeforeEach(func() {
			taskRecord, createErr = taskRepo.CreateTask(ctx, authInfo, createMessage)
		})

		It("returns forbidden error", func() {
			Expect(createErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.ForbiddenError{}))
		})

		When("the user can create tasks", func() {
			BeforeEach(func() {
				createRoleBinding(ctx, userName, spaceDeveloperRole.Name, space.Name)
			})

			It("creates the task", func() {
				Expect(createErr).NotTo(HaveOccurred())
				Expect(taskRecord.Name).NotTo(BeEmpty())
				Expect(taskRecord.GUID).NotTo(BeEmpty())
				Expect(taskRecord.Command).To(Equal("echo 'hello world'"))
				Expect(taskRecord.AppGUID).To(Equal(cfApp.Name))
				Expect(taskRecord.SequenceID).NotTo(BeZero())

				Expect(taskRecord.CreatedAt).To(BeTemporally("~", time.Now(), timeCheckThreshold))
				Expect(taskRecord.UpdatedAt).To(gstruct.PointTo(BeTemporally("~", time.Now(), timeCheckThreshold)))

				Expect(taskRecord.MemoryMB).To(BeEquivalentTo(256))
				Expect(taskRecord.DiskMB).To(BeEquivalentTo(128))
				Expect(taskRecord.DropletGUID).To(Equal(cfApp.Spec.CurrentDropletRef.Name))
				Expect(taskRecord.State).To(Equal(repositories.TaskStatePending))
				Expect(taskRecord.Labels).To(Equal(map[string]string{"color": "blue"}))
				Expect(taskRecord.Annotations).To(Equal(map[string]string{"extra-bugs": "true"}))
			})

			When("the task never becomes initialized", func() {
				BeforeEach(func() {
					simTaskController = func(cft *korifiv1alpha1.CFTask) {}
				})

				It("returns an error", func() {
					Expect(createErr).To(MatchError(ContainSubstring("did not get the Initialized condition")))
				})
			})
		})

		When("unprivileged client creation fails", func() {
			BeforeEach(func() {
				authInfo = authorization.Info{}
			})

			It("returns an error", func() {
				Expect(createErr).To(MatchError(ContainSubstring("failed to build user client")))
			})
		})
	})

	Describe("GetTask", func() {
		var (
			taskRecord repositories.TaskRecord
			getErr     error
			taskGUID   string
			cfTask     *korifiv1alpha1.CFTask
		)

		BeforeEach(func() {
			taskGUID = generateGUID()
			cfTask = &korifiv1alpha1.CFTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      taskGUID,
					Namespace: space.Name,
				},
				Spec: korifiv1alpha1.CFTaskSpec{
					Command: "echo hello",
					AppRef: corev1.LocalObjectReference{
						Name: cfApp.Name,
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), cfTask)).To(Succeed())

			setStatusAndUpdate(defaultStatusValues(cfTask, 6, cfApp.Spec.CurrentDropletRef.Name))
		})

		JustBeforeEach(func() {
			taskRecord, getErr = taskRepo.GetTask(ctx, authInfo, taskGUID)
		})

		It("returns a forbidden error", func() {
			Expect(getErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.ForbiddenError{}))
		})

		When("the user can get tasks", func() {
			BeforeEach(func() {
				createRoleBinding(ctx, userName, spaceDeveloperRole.Name, space.Name)
			})

			When("the task has not been initialized yet", func() {
				It("returns a not found error", func() {
					Expect(getErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.NotFoundError{}))
				})
			})

			When("the task is ready", func() {
				BeforeEach(func() {
					setStatusAndUpdate(
						defaultStatusValues(cfTask, 6, cfApp.Spec.CurrentDropletRef.Name),
						korifiv1alpha1.TaskInitializedConditionType,
					)
				})

				It("returns the task", func() {
					Expect(getErr).NotTo(HaveOccurred())
					Expect(taskRecord.Name).To(Equal(taskGUID))
					Expect(taskRecord.GUID).NotTo(BeEmpty())
					Expect(taskRecord.Command).To(Equal("echo hello"))
					Expect(taskRecord.AppGUID).To(Equal(cfApp.Name))
					Expect(taskRecord.SequenceID).To(BeEquivalentTo(6))

					Expect(taskRecord.CreatedAt).To(BeTemporally("~", time.Now(), timeCheckThreshold))
					Expect(taskRecord.UpdatedAt).To(gstruct.PointTo(BeTemporally("~", time.Now(), timeCheckThreshold)))

					Expect(taskRecord.MemoryMB).To(BeEquivalentTo(256))
					Expect(taskRecord.DiskMB).To(BeEquivalentTo(128))
					Expect(taskRecord.DropletGUID).To(Equal(cfApp.Spec.CurrentDropletRef.Name))
					Expect(taskRecord.State).To(Equal(repositories.TaskStatePending))
				})
			})

			When("the task is running", func() {
				BeforeEach(func() {
					setStatusAndUpdate(cfTask, korifiv1alpha1.TaskInitializedConditionType, korifiv1alpha1.TaskStartedConditionType)
				})

				It("returns the running task", func() {
					Expect(getErr).NotTo(HaveOccurred())
					Expect(taskRecord.State).To(Equal(repositories.TaskStateRunning))
				})
			})

			When("the task has succeeded", func() {
				BeforeEach(func() {
					setStatusAndUpdate(cfTask, korifiv1alpha1.TaskInitializedConditionType, korifiv1alpha1.TaskStartedConditionType, korifiv1alpha1.TaskSucceededConditionType)
				})

				It("returns the succeeded task", func() {
					Expect(getErr).NotTo(HaveOccurred())
					Expect(taskRecord.State).To(Equal(repositories.TaskStateSucceeded))
				})
			})

			When("the task has failed", func() {
				BeforeEach(func() {
					setStatusAndUpdate(cfTask, korifiv1alpha1.TaskInitializedConditionType, korifiv1alpha1.TaskStartedConditionType, korifiv1alpha1.TaskFailedConditionType)
				})

				It("returns the failed task", func() {
					Expect(getErr).NotTo(HaveOccurred())
					Expect(taskRecord.State).To(Equal(repositories.TaskStateFailed))
					Expect(taskRecord.FailureReason).To(Equal("bar"))
				})
			})

			When("the task was cancelled", func() {
				BeforeEach(func() {
					setStatusAndUpdate(cfTask, korifiv1alpha1.TaskInitializedConditionType, korifiv1alpha1.TaskStartedConditionType)
					meta.SetStatusCondition(&(cfTask.Status.Conditions), metav1.Condition{
						Type:   korifiv1alpha1.TaskFailedConditionType,
						Status: metav1.ConditionTrue,
						Reason: "TaskCanceled",
					})
					Expect(k8sClient.Status().Update(ctx, cfTask)).To(Succeed())
				})

				It("returns the failed task", func() {
					Expect(getErr).NotTo(HaveOccurred())
					Expect(taskRecord.State).To(Equal(repositories.TaskStateFailed))
					Expect(taskRecord.FailureReason).To(Equal("task was cancelled"))
				})
			})
		})

		When("the task doesn't exist", func() {
			BeforeEach(func() {
				taskGUID = "does-not-exist"
			})

			It("returns a not found error", func() {
				Expect(getErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.NotFoundError{}))
			})
		})

		When("unprivileged client creation fails", func() {
			BeforeEach(func() {
				authInfo = authorization.Info{}
			})

			It("returns an error", func() {
				Expect(getErr).To(MatchError(ContainSubstring("failed to build user client")))
			})
		})
	})

	Describe("List Tasks", func() {
		var (
			cfApp2      *korifiv1alpha1.CFApp
			task1       *korifiv1alpha1.CFTask
			task2       *korifiv1alpha1.CFTask
			space2      *korifiv1alpha1.CFSpace
			listTaskMsg repositories.ListTaskMessage

			listedTasks []repositories.TaskRecord
			listErr     error
		)

		BeforeEach(func() {
			space2 = createSpaceWithCleanup(ctx, org.Name, prefixedGUID("space2"))
			cfApp2 = createApp(space2.Name)
			listTaskMsg = repositories.ListTaskMessage{}

			task1 = &korifiv1alpha1.CFTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      prefixedGUID("task1"),
					Namespace: space.Name,
				},
				Spec: korifiv1alpha1.CFTaskSpec{
					Command: "echo hello",
					AppRef: corev1.LocalObjectReference{
						Name: cfApp.Name,
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), task1)).To(Succeed())

			task2 = &korifiv1alpha1.CFTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      prefixedGUID("task2"),
					Namespace: space2.Name,
				},
				Spec: korifiv1alpha1.CFTaskSpec{
					Command: "echo hello",
					AppRef: corev1.LocalObjectReference{
						Name: cfApp2.Name,
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), task2)).To(Succeed())
		})

		JustBeforeEach(func() {
			listedTasks, listErr = taskRepo.ListTasks(ctx, authInfo, listTaskMsg)
		})

		It("returns an empty list due to no permissions", func() {
			Expect(listErr).NotTo(HaveOccurred())
			Expect(listedTasks).To(BeEmpty())
		})

		When("the user has the space developer role in space2", func() {
			BeforeEach(func() {
				createRoleBinding(ctx, userName, spaceDeveloperRole.Name, space2.Name)
			})

			It("lists tasks from that namespace only", func() {
				Expect(listErr).NotTo(HaveOccurred())
				Expect(listedTasks).To(HaveLen(1))
				Expect(listedTasks[0].Name).To(Equal(task2.Name))
			})

			When("the user has a useless binding in space1", func() {
				BeforeEach(func() {
					createRoleBinding(ctx, userName, rootNamespaceUserRole.Name, space.Name)
				})

				It("still lists tasks from that namespace only", func() {
					Expect(listErr).NotTo(HaveOccurred())
					Expect(listedTasks).To(HaveLen(1))
					Expect(listedTasks[0].Name).To(Equal(task2.Name))
				})
			})

			When("filtering tasks by apps with permissions for both", func() {
				BeforeEach(func() {
					createRoleBinding(ctx, userName, spaceDeveloperRole.Name, space.Name)
				})

				When("the app1 guid is passed as a filter", func() {
					BeforeEach(func() {
						listTaskMsg.AppGUIDs = []string{cfApp.Name}
					})

					It("lists tasks for that app", func() {
						Expect(listErr).NotTo(HaveOccurred())
						Expect(listedTasks).To(HaveLen(1))
						Expect(listedTasks[0].Name).To(Equal(task1.Name))
					})
				})

				When("the app2 guid is passed as a filter", func() {
					BeforeEach(func() {
						listTaskMsg.AppGUIDs = []string{cfApp2.Name}
					})

					It("lists tasks for that app", func() {
						Expect(listErr).NotTo(HaveOccurred())
						Expect(listedTasks).To(HaveLen(1))
						Expect(listedTasks[0].Name).To(Equal(task2.Name))
					})
				})

				When("app guid and sequence IDs are passed as a filter", func() {
					BeforeEach(func() {
						setStatusAndUpdate(
							defaultStatusValues(task2, 2, cfApp2.Spec.CurrentDropletRef.Name),
							korifiv1alpha1.TaskInitializedConditionType,
						)

						task21 := &korifiv1alpha1.CFTask{
							ObjectMeta: metav1.ObjectMeta{
								Name:      prefixedGUID("task21"),
								Namespace: space2.Name,
							},
							Spec: korifiv1alpha1.CFTaskSpec{
								Command: "echo hello",
								AppRef: corev1.LocalObjectReference{
									Name: cfApp2.Name,
								},
							},
						}
						Expect(k8sClient.Create(context.Background(), task21)).To(Succeed())
						setStatusAndUpdate(
							defaultStatusValues(task21, 21, cfApp2.Spec.CurrentDropletRef.Name),
							korifiv1alpha1.TaskInitializedConditionType,
						)

						listTaskMsg.AppGUIDs = []string{cfApp2.Name}
						listTaskMsg.SequenceIDs = []int64{2}
					})

					It("returns the tasks filtered by sequence ID", func() {
						Expect(listErr).NotTo(HaveOccurred())
						Expect(listedTasks).To(HaveLen(1))
						Expect(listedTasks[0].Name).To(Equal(task2.Name))
					})
				})

				When("filtering by a non-existant app guid", func() {
					BeforeEach(func() {
						listTaskMsg.AppGUIDs = []string{"does-not-exist"}
					})

					It("returns an empty list", func() {
						Expect(listErr).NotTo(HaveOccurred())
						Expect(listedTasks).To(BeEmpty())
					})
				})
			})
		})
	})

	Describe("Cancel Task", func() {
		var (
			taskGUID   string
			cancelErr  error
			taskRecord repositories.TaskRecord
		)

		BeforeEach(func() {
			simTaskController = func(cft *korifiv1alpha1.CFTask) {
				if cft.Spec.Canceled {
					setStatusAndUpdate(cft, korifiv1alpha1.TaskCanceledConditionType)
				}
			}

			taskGUID = generateGUID()
			cfTask := &korifiv1alpha1.CFTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      taskGUID,
					Namespace: space.Name,
				},
				Spec: korifiv1alpha1.CFTaskSpec{
					Command: "echo hello",
					AppRef: corev1.LocalObjectReference{
						Name: cfApp.Name,
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), cfTask)).To(Succeed())

			cfTask.Status.SequenceID = 6
			cfTask.Status.MemoryMB = 256
			cfTask.Status.DiskQuotaMB = 128
			cfTask.Status.DropletRef.Name = cfApp.Spec.CurrentDropletRef.Name
			setStatusAndUpdate(cfTask, korifiv1alpha1.TaskInitializedConditionType, korifiv1alpha1.TaskStartedConditionType)
		})

		JustBeforeEach(func() {
			taskRecord, cancelErr = taskRepo.CancelTask(ctx, authInfo, taskGUID)
		})

		It("returns forbidden", func() {
			Expect(cancelErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.ForbiddenError{}))
		})

		When("the user is a space developer", func() {
			BeforeEach(func() {
				createRoleBinding(ctx, userName, spaceDeveloperRole.Name, space.Name)
			})

			It("cancels the task", func() {
				Expect(cancelErr).NotTo(HaveOccurred())
				Expect(taskRecord.Name).To(Equal(taskGUID))
				Expect(taskRecord.GUID).NotTo(BeEmpty())
				Expect(taskRecord.Command).To(Equal("echo hello"))
				Expect(taskRecord.AppGUID).To(Equal(cfApp.Name))
				Expect(taskRecord.SequenceID).To(BeEquivalentTo(6))

				Expect(taskRecord.CreatedAt).To(BeTemporally("~", time.Now(), timeCheckThreshold))
				Expect(taskRecord.UpdatedAt).To(gstruct.PointTo(BeTemporally("~", time.Now(), timeCheckThreshold)))

				Expect(taskRecord.MemoryMB).To(BeEquivalentTo(256))
				Expect(taskRecord.DiskMB).To(BeEquivalentTo(128))
				Expect(taskRecord.DropletGUID).To(Equal(cfApp.Spec.CurrentDropletRef.Name))
				Expect(taskRecord.State).To(Equal(repositories.TaskStateCanceling))
			})

			When("the status is not updated within the timeout", func() {
				BeforeEach(func() {
					simTaskController = func(*korifiv1alpha1.CFTask) {}
				})

				It("returns a timeout error", func() {
					Expect(cancelErr).To(MatchError(ContainSubstring("did not get the Canceled condition")))
				})
			})
		})
	})

	Describe("PatchTaskMetadata", func() {
		var (
			cfTask                        *korifiv1alpha1.CFTask
			taskGUID                      string
			patchErr                      error
			taskRecord                    repositories.TaskRecord
			labelsPatch, annotationsPatch map[string]*string
		)

		BeforeEach(func() {
			taskGUID = generateGUID()
			cfTask = &korifiv1alpha1.CFTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      taskGUID,
					Namespace: space.Name,
				},
				Spec: korifiv1alpha1.CFTaskSpec{
					Command: "echo hello",
					AppRef: corev1.LocalObjectReference{
						Name: cfApp.Name,
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), cfTask)).To(Succeed())

			setStatusAndUpdate(defaultStatusValues(cfTask, 6, cfApp.Spec.CurrentDropletRef.Name))

			labelsPatch = nil
			annotationsPatch = nil
		})

		JustBeforeEach(func() {
			patchMsg := repositories.PatchTaskMetadataMessage{
				TaskGUID:  taskGUID,
				SpaceGUID: space.Name,
				MetadataPatch: repositories.MetadataPatch{
					Annotations: annotationsPatch,
					Labels:      labelsPatch,
				},
			}

			taskRecord, patchErr = taskRepo.PatchTaskMetadata(ctx, authInfo, patchMsg)
		})

		When("the user is authorized and the task exists", func() {
			BeforeEach(func() {
				createRoleBinding(ctx, userName, spaceDeveloperRole.Name, space.Name)
			})

			When("the task doesn't have labels or annotations", func() {
				BeforeEach(func() {
					labelsPatch = map[string]*string{
						"key-one": pointerTo("value-one"),
						"key-two": pointerTo("value-two"),
					}
					annotationsPatch = map[string]*string{
						"key-one": pointerTo("value-one"),
						"key-two": pointerTo("value-two"),
					}
					Expect(k8s.PatchResource(ctx, k8sClient, cfTask, func() {
						cfTask.Labels = nil
						cfTask.Annotations = nil
					})).To(Succeed())
				})

				It("returns the updated org record", func() {
					Expect(patchErr).NotTo(HaveOccurred())
					Expect(taskRecord.GUID).To(Equal(taskGUID))
					Expect(taskRecord.SpaceGUID).To(Equal(space.Name))
					Expect(taskRecord.Labels).To(Equal(
						map[string]string{
							"key-one": "value-one",
							"key-two": "value-two",
						},
					))
					Expect(taskRecord.Annotations).To(Equal(
						map[string]string{
							"key-one": "value-one",
							"key-two": "value-two",
						},
					))
				})

				It("sets the k8s CFSpace resource", func() {
					Expect(patchErr).NotTo(HaveOccurred())
					updatedCFTask := new(korifiv1alpha1.CFTask)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cfTask), updatedCFTask)).To(Succeed())
					Expect(updatedCFTask.Labels).To(Equal(
						map[string]string{
							"key-one": "value-one",
							"key-two": "value-two",
						},
					))
					Expect(updatedCFTask.Annotations).To(Equal(
						map[string]string{
							"key-one": "value-one",
							"key-two": "value-two",
						},
					))
				})
			})

			When("the task already has labels and annotations", func() {
				BeforeEach(func() {
					labelsPatch = map[string]*string{
						"key-one":        pointerTo("value-one-updated"),
						"key-two":        pointerTo("value-two"),
						"before-key-two": nil,
					}
					annotationsPatch = map[string]*string{
						"key-one":        pointerTo("value-one-updated"),
						"key-two":        pointerTo("value-two"),
						"before-key-two": nil,
					}
					Expect(k8s.PatchResource(ctx, k8sClient, cfTask, func() {
						cfTask.Labels = map[string]string{
							"before-key-one": "value-one",
							"before-key-two": "value-two",
							"key-one":        "value-one",
						}
						cfTask.Annotations = map[string]string{
							"before-key-one": "value-one",
							"before-key-two": "value-two",
							"key-one":        "value-one",
						}
					})).To(Succeed())
				})

				It("returns the updated task record", func() {
					Expect(patchErr).NotTo(HaveOccurred())
					Expect(taskRecord.GUID).To(Equal(cfTask.Name))
					Expect(taskRecord.SpaceGUID).To(Equal(cfTask.Namespace))
					Expect(taskRecord.Labels).To(Equal(
						map[string]string{
							"before-key-one": "value-one",
							"key-one":        "value-one-updated",
							"key-two":        "value-two",
						},
					))
					Expect(taskRecord.Annotations).To(Equal(
						map[string]string{
							"before-key-one": "value-one",
							"key-one":        "value-one-updated",
							"key-two":        "value-two",
						},
					))
				})

				It("sets the k8s cftask resource", func() {
					Expect(patchErr).NotTo(HaveOccurred())
					updatedCFTask := new(korifiv1alpha1.CFTask)
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cfTask), updatedCFTask)).To(Succeed())
					Expect(updatedCFTask.Labels).To(Equal(
						map[string]string{
							"before-key-one": "value-one",
							"key-one":        "value-one-updated",
							"key-two":        "value-two",
						},
					))
					Expect(updatedCFTask.Annotations).To(Equal(
						map[string]string{
							"before-key-one": "value-one",
							"key-one":        "value-one-updated",
							"key-two":        "value-two",
						},
					))
				})
			})

			When("an annotation is invalid", func() {
				BeforeEach(func() {
					annotationsPatch = map[string]*string{
						"-bad-annotation": pointerTo("stuff"),
					}
				})

				It("returns an UnprocessableEntityError", func() {
					var unprocessableEntityError apierrors.UnprocessableEntityError
					Expect(errors.As(patchErr, &unprocessableEntityError)).To(BeTrue())
					Expect(unprocessableEntityError.Detail()).To(SatisfyAll(
						ContainSubstring("metadata.annotations is invalid"),
						ContainSubstring(`"-bad-annotation"`),
						ContainSubstring("alphanumeric"),
					))
				})
			})

			When("a label is invalid", func() {
				BeforeEach(func() {
					labelsPatch = map[string]*string{
						"-bad-label": pointerTo("stuff"),
					}
				})

				It("returns an UnprocessableEntityError", func() {
					var unprocessableEntityError apierrors.UnprocessableEntityError
					Expect(errors.As(patchErr, &unprocessableEntityError)).To(BeTrue())
					Expect(unprocessableEntityError.Detail()).To(SatisfyAll(
						ContainSubstring("metadata.labels is invalid"),
						ContainSubstring(`"-bad-label"`),
						ContainSubstring("alphanumeric"),
					))
				})
			})
		})

		When("the user is authorized but the task does not exist", func() {
			BeforeEach(func() {
				createRoleBinding(ctx, userName, spaceDeveloperRole.Name, space.Name)
				taskGUID = "invalidTaskName"
			})

			It("fails to get the task", func() {
				Expect(patchErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.NotFoundError{}))
			})
		})

		When("the user is not authorized", func() {
			It("return a forbidden error", func() {
				Expect(patchErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.ForbiddenError{}))
			})
		})
	})
})
