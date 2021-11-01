// Code generated by counterfeiter. DO NOT EDIT.
package fake

import (
	"context"
	"sync"

	"code.cloudfoundry.org/cf-k8s-api/apis"
	"code.cloudfoundry.org/cf-k8s-api/repositories"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CFProcessRepository struct {
	FetchProcessStub        func(context.Context, client.Client, string) (repositories.ProcessRecord, error)
	fetchProcessMutex       sync.RWMutex
	fetchProcessArgsForCall []struct {
		arg1 context.Context
		arg2 client.Client
		arg3 string
	}
	fetchProcessReturns struct {
		result1 repositories.ProcessRecord
		result2 error
	}
	fetchProcessReturnsOnCall map[int]struct {
		result1 repositories.ProcessRecord
		result2 error
	}
	FetchProcessesForAppStub        func(context.Context, client.Client, string, string) ([]repositories.ProcessRecord, error)
	fetchProcessesForAppMutex       sync.RWMutex
	fetchProcessesForAppArgsForCall []struct {
		arg1 context.Context
		arg2 client.Client
		arg3 string
		arg4 string
	}
	fetchProcessesForAppReturns struct {
		result1 []repositories.ProcessRecord
		result2 error
	}
	fetchProcessesForAppReturnsOnCall map[int]struct {
		result1 []repositories.ProcessRecord
		result2 error
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *CFProcessRepository) FetchProcess(arg1 context.Context, arg2 client.Client, arg3 string) (repositories.ProcessRecord, error) {
	fake.fetchProcessMutex.Lock()
	ret, specificReturn := fake.fetchProcessReturnsOnCall[len(fake.fetchProcessArgsForCall)]
	fake.fetchProcessArgsForCall = append(fake.fetchProcessArgsForCall, struct {
		arg1 context.Context
		arg2 client.Client
		arg3 string
	}{arg1, arg2, arg3})
	stub := fake.FetchProcessStub
	fakeReturns := fake.fetchProcessReturns
	fake.recordInvocation("FetchProcess", []interface{}{arg1, arg2, arg3})
	fake.fetchProcessMutex.Unlock()
	if stub != nil {
		return stub(arg1, arg2, arg3)
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *CFProcessRepository) FetchProcessCallCount() int {
	fake.fetchProcessMutex.RLock()
	defer fake.fetchProcessMutex.RUnlock()
	return len(fake.fetchProcessArgsForCall)
}

func (fake *CFProcessRepository) FetchProcessCalls(stub func(context.Context, client.Client, string) (repositories.ProcessRecord, error)) {
	fake.fetchProcessMutex.Lock()
	defer fake.fetchProcessMutex.Unlock()
	fake.FetchProcessStub = stub
}

func (fake *CFProcessRepository) FetchProcessArgsForCall(i int) (context.Context, client.Client, string) {
	fake.fetchProcessMutex.RLock()
	defer fake.fetchProcessMutex.RUnlock()
	argsForCall := fake.fetchProcessArgsForCall[i]
	return argsForCall.arg1, argsForCall.arg2, argsForCall.arg3
}

func (fake *CFProcessRepository) FetchProcessReturns(result1 repositories.ProcessRecord, result2 error) {
	fake.fetchProcessMutex.Lock()
	defer fake.fetchProcessMutex.Unlock()
	fake.FetchProcessStub = nil
	fake.fetchProcessReturns = struct {
		result1 repositories.ProcessRecord
		result2 error
	}{result1, result2}
}

func (fake *CFProcessRepository) FetchProcessReturnsOnCall(i int, result1 repositories.ProcessRecord, result2 error) {
	fake.fetchProcessMutex.Lock()
	defer fake.fetchProcessMutex.Unlock()
	fake.FetchProcessStub = nil
	if fake.fetchProcessReturnsOnCall == nil {
		fake.fetchProcessReturnsOnCall = make(map[int]struct {
			result1 repositories.ProcessRecord
			result2 error
		})
	}
	fake.fetchProcessReturnsOnCall[i] = struct {
		result1 repositories.ProcessRecord
		result2 error
	}{result1, result2}
}

func (fake *CFProcessRepository) FetchProcessesForApp(arg1 context.Context, arg2 client.Client, arg3 string, arg4 string) ([]repositories.ProcessRecord, error) {
	fake.fetchProcessesForAppMutex.Lock()
	ret, specificReturn := fake.fetchProcessesForAppReturnsOnCall[len(fake.fetchProcessesForAppArgsForCall)]
	fake.fetchProcessesForAppArgsForCall = append(fake.fetchProcessesForAppArgsForCall, struct {
		arg1 context.Context
		arg2 client.Client
		arg3 string
		arg4 string
	}{arg1, arg2, arg3, arg4})
	stub := fake.FetchProcessesForAppStub
	fakeReturns := fake.fetchProcessesForAppReturns
	fake.recordInvocation("FetchProcessesForApp", []interface{}{arg1, arg2, arg3, arg4})
	fake.fetchProcessesForAppMutex.Unlock()
	if stub != nil {
		return stub(arg1, arg2, arg3, arg4)
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *CFProcessRepository) FetchProcessesForAppCallCount() int {
	fake.fetchProcessesForAppMutex.RLock()
	defer fake.fetchProcessesForAppMutex.RUnlock()
	return len(fake.fetchProcessesForAppArgsForCall)
}

func (fake *CFProcessRepository) FetchProcessesForAppCalls(stub func(context.Context, client.Client, string, string) ([]repositories.ProcessRecord, error)) {
	fake.fetchProcessesForAppMutex.Lock()
	defer fake.fetchProcessesForAppMutex.Unlock()
	fake.FetchProcessesForAppStub = stub
}

func (fake *CFProcessRepository) FetchProcessesForAppArgsForCall(i int) (context.Context, client.Client, string, string) {
	fake.fetchProcessesForAppMutex.RLock()
	defer fake.fetchProcessesForAppMutex.RUnlock()
	argsForCall := fake.fetchProcessesForAppArgsForCall[i]
	return argsForCall.arg1, argsForCall.arg2, argsForCall.arg3, argsForCall.arg4
}

func (fake *CFProcessRepository) FetchProcessesForAppReturns(result1 []repositories.ProcessRecord, result2 error) {
	fake.fetchProcessesForAppMutex.Lock()
	defer fake.fetchProcessesForAppMutex.Unlock()
	fake.FetchProcessesForAppStub = nil
	fake.fetchProcessesForAppReturns = struct {
		result1 []repositories.ProcessRecord
		result2 error
	}{result1, result2}
}

func (fake *CFProcessRepository) FetchProcessesForAppReturnsOnCall(i int, result1 []repositories.ProcessRecord, result2 error) {
	fake.fetchProcessesForAppMutex.Lock()
	defer fake.fetchProcessesForAppMutex.Unlock()
	fake.FetchProcessesForAppStub = nil
	if fake.fetchProcessesForAppReturnsOnCall == nil {
		fake.fetchProcessesForAppReturnsOnCall = make(map[int]struct {
			result1 []repositories.ProcessRecord
			result2 error
		})
	}
	fake.fetchProcessesForAppReturnsOnCall[i] = struct {
		result1 []repositories.ProcessRecord
		result2 error
	}{result1, result2}
}

func (fake *CFProcessRepository) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.fetchProcessMutex.RLock()
	defer fake.fetchProcessMutex.RUnlock()
	fake.fetchProcessesForAppMutex.RLock()
	defer fake.fetchProcessesForAppMutex.RUnlock()
	copiedInvocations := map[string][][]interface{}{}
	for key, value := range fake.invocations {
		copiedInvocations[key] = value
	}
	return copiedInvocations
}

func (fake *CFProcessRepository) recordInvocation(key string, args []interface{}) {
	fake.invocationsMutex.Lock()
	defer fake.invocationsMutex.Unlock()
	if fake.invocations == nil {
		fake.invocations = map[string][][]interface{}{}
	}
	if fake.invocations[key] == nil {
		fake.invocations[key] = [][]interface{}{}
	}
	fake.invocations[key] = append(fake.invocations[key], args)
}

var _ apis.CFProcessRepository = new(CFProcessRepository)
