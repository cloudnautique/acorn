package resolvedofferings

import (
	"github.com/acorn-io/baaah/pkg/router"
	apiv1 "github.com/acorn-io/runtime/pkg/apis/api.acorn.io/v1"
	v1 "github.com/acorn-io/runtime/pkg/apis/internal.acorn.io/v1"
	adminv1 "github.com/acorn-io/runtime/pkg/apis/internal.admin.acorn.io/v1"
	"github.com/acorn-io/runtime/pkg/computeclasses"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// resolveComputeClasses resolves the compute class information for each container in the AppInstance
func resolveComputeClasses(req router.Request, cfg *apiv1.Config, appInstance *v1.AppInstance) error {
	if appInstance.Status.ResolvedOfferings.Containers == nil {
		appInstance.Status.ResolvedOfferings.Containers = map[string]v1.ContainerResolvedOffering{}
	}

	var (
		defaultCC string
		err       error
	)
	if value, ok := appInstance.Spec.ComputeClasses[""]; ok {
		defaultCC = value
	} else {
		defaultCC, err = adminv1.GetDefaultComputeClass(req.Ctx, req.Client, appInstance.Namespace)
		if err != nil {
			return err
		}
	}

	// Set the default for all containers, noted by the empty string
	appInstance.Status.ResolvedOfferings.Containers[""] = v1.ContainerResolvedOffering{
		Memory: cfg.WorkloadMemoryDefault,
		Class:  defaultCC,
	}
	cc, err := computeclasses.GetAsProjectComputeClassInstance(req.Ctx, req.Client, appInstance.Status.Namespace, defaultCC)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if cc != nil {
		parsedMemory, err := computeclasses.ParseComputeClassMemoryInternal(cc.Memory)
		if err != nil {
			return err
		}
		def := parsedMemory.Def.Value()
		appInstance.Status.ResolvedOfferings.Containers[""] = v1.ContainerResolvedOffering{
			Memory:    &def,
			CPUScaler: &cc.CPUScaler,
			Class:     appInstance.Status.ResolvedOfferings.Containers[""].Class,
		}
	}

	// Check to see if the user overrode the memory for all containers
	if appInstance.Spec.Memory[""] != nil {
		appInstance.Status.ResolvedOfferings.Containers[""] = v1.ContainerResolvedOffering{
			Memory:    appInstance.Spec.Memory[""],
			CPUScaler: appInstance.Status.ResolvedOfferings.Containers[""].CPUScaler,
			Class:     appInstance.Status.ResolvedOfferings.Containers[""].Class,
		}
	}

	// Set the compute class info for each container and job individually
	if err := resolveComputeClass(req, appInstance, cfg.WorkloadMemoryDefault, cc, defaultCC, appInstance.Status.AppSpec.Containers); err != nil {
		return err
	}

	if err := resolveComputeClass(req, appInstance, cfg.WorkloadMemoryDefault, cc, defaultCC, appInstance.Status.AppSpec.Jobs); err != nil {
		return err
	}

	return nil
}

func resolveComputeClass(req router.Request, appInstance *v1.AppInstance, configDefault *int64, defaultCC *adminv1.ProjectComputeClassInstance, defaultCCName string, containers map[string]v1.Container) error {
	for name, container := range containers {
		var cpuScaler *float64
		ccName := ""

		// First, get the compute class for the workload
		cc, err := computeclasses.GetClassForWorkload(req.Ctx, req.Client, appInstance.Spec.ComputeClasses, container, name, appInstance.Namespace)
		if err != nil {
			return err
		}
		if cc == nil {
			cc = defaultCC
		}
		if cc != nil {
			ccName = cc.Name
			cpuScaler = &cc.CPUScaler
		} else {
			ccName = defaultCCName
		}

		// Next, determine the memory request. This is the order of priority:
		// 1. runtime-level overrides from the user (in app.Spec)
		// 2. defaults in the acorn image
		// 3. defaults from compute class
		// 4. global default

		memory := configDefault // set to global default first, then check the higher priority values

		if appInstance.Spec.Memory[name] != nil { // runtime-level overrides from the user
			memory = appInstance.Spec.Memory[name]
		} else if appInstance.Spec.Memory[""] != nil { // runtime-level overrides from the user for all containers in the app
			memory = appInstance.Spec.Memory[""]
		} else if container.Memory != nil { // defaults in the acorn image
			memory = container.Memory
		} else if cc != nil { // defaults from compute class
			parsedMemory, err := computeclasses.ParseComputeClassMemoryInternal(cc.Memory)
			if err != nil {
				return err
			}
			def := parsedMemory.Def.Value()
			memory = &def
		}

		appInstance.Status.ResolvedOfferings.Containers[name] = v1.ContainerResolvedOffering{
			Class:     ccName,
			Memory:    memory,
			CPUScaler: cpuScaler,
		}

		for sidecarName := range container.Sidecars {
			appInstance.Status.ResolvedOfferings.Containers[sidecarName] = v1.ContainerResolvedOffering{
				Class:     ccName,
				Memory:    memory,
				CPUScaler: cpuScaler,
			}
		}
	}

	return nil
}
