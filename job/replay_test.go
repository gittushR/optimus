package job_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"

	"github.com/odpf/optimus/job"

	"github.com/odpf/optimus/core/tree"

	"github.com/pkg/errors"

	"github.com/odpf/optimus/mock"
	"github.com/odpf/optimus/models"
	"github.com/stretchr/testify/assert"
)

func getRuns(root *tree.TreeNode, countMap map[string][]time.Time) {
	if _, ok := countMap[root.GetName()]; ok {
		return
	}
	for _, val := range root.Runs.Values() {
		run := val.(time.Time)
		if _, found := countMap[root.GetName()]; !found {
			countMap[root.GetName()] = []time.Time{run}
		} else {
			countMap[root.GetName()] = append(countMap[root.GetName()], run)
		}
	}
	for _, dep := range root.Dependents {
		getRuns(dep, countMap)
	}
}

func getRunsWithStatus(root *tree.TreeNode, countMap map[string][]models.JobStatus) {
	if _, ok := countMap[root.GetName()]; ok {
		return
	}
	for _, val := range root.Runs.Values() {
		jobStatus := val.(models.JobStatus)
		if _, found := countMap[root.GetName()]; !found {
			countMap[root.GetName()] = []models.JobStatus{jobStatus}
		} else {
			countMap[root.GetName()] = append(countMap[root.GetName()], jobStatus)
		}
	}
	for _, dep := range root.Dependents {
		getRunsWithStatus(dep, countMap)
	}
}

func TestReplay(t *testing.T) {
	ctx := context.TODO()
	noDependency := map[string]models.JobSpecDependency{}
	dumpAssets := func(jobSpec models.JobSpec, scheduledAt time.Time) (models.JobAssets, error) {
		return jobSpec.Assets, nil
	}
	var (
		specs   = make(map[string]models.JobSpec)
		dagSpec = make([]models.JobSpec, 0)
	)

	dagStartTime, _ := time.Parse(job.ReplayDateFormat, "2020-04-05")
	spec1 := "dag1-no-deps"
	spec2 := "dag2-deps-on-dag1"
	spec3 := "dag3-deps-on-dag2"
	spec4 := "dag4-no-deps"
	spec5 := "dag5-deps-on-dag4"
	spec6 := "dag6-deps-on-dag4-and-dag5"

	twoAMSchedule := models.JobSpecSchedule{
		StartDate: dagStartTime,
		Interval:  "0 2 * * *",
	}
	hourlySchedule := models.JobSpecSchedule{
		StartDate: dagStartTime,
		Interval:  "@hourly",
	}
	dailySchedule := models.JobSpecSchedule{
		StartDate: dagStartTime,
		Interval:  "@daily",
	}
	oneDayTaskWindow := models.JobSpecTask{
		Window: models.JobSpecTaskWindow{
			Size: time.Hour * 24,
		},
	}
	threeDayTaskWindow := models.JobSpecTask{
		Window: models.JobSpecTaskWindow{
			Size: time.Hour * 24 * 3,
		},
	}

	specs[spec1] = models.JobSpec{Name: spec1, Dependencies: noDependency, Schedule: twoAMSchedule, Task: oneDayTaskWindow}
	dagSpec = append(dagSpec, specs[spec1])
	specs[spec2] = models.JobSpec{Name: spec2, Dependencies: getDependencyObject(specs, spec1), Schedule: twoAMSchedule, Task: threeDayTaskWindow}
	dagSpec = append(dagSpec, specs[spec2])
	specs[spec3] = models.JobSpec{Name: spec3, Dependencies: getDependencyObject(specs, spec2), Schedule: twoAMSchedule, Task: threeDayTaskWindow}
	dagSpec = append(dagSpec, specs[spec3])
	specs[spec4] = models.JobSpec{Name: spec4, Dependencies: noDependency, Schedule: hourlySchedule, Task: threeDayTaskWindow}
	dagSpec = append(dagSpec, specs[spec4])
	specs[spec5] = models.JobSpec{Name: spec5, Dependencies: getDependencyObject(specs, spec4), Schedule: dailySchedule, Task: threeDayTaskWindow}
	dagSpec = append(dagSpec, specs[spec5])
	specs[spec6] = models.JobSpec{Name: spec6, Dependencies: getDependencyObject(specs, spec4, spec5), Schedule: dailySchedule, Task: threeDayTaskWindow}
	dagSpec = append(dagSpec, specs[spec6])
	projSpec := models.ProjectSpec{
		Name: "proj",
	}

	t.Run("ReplayDryRun", func(t *testing.T) {
		t.Run("should fail if unable to fetch jobSpecs from project jobSpecRepo", func(t *testing.T) {
			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(nil, errors.New("error while getting all dags"))
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")

			jobSvc := job.NewService(nil, nil, nil, dumpAssets, nil, nil, nil, projJobSpecRepoFac, nil)
			replayRequest := &models.ReplayRequest{
				Job:     specs[spec1],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}
			_, err := jobSvc.ReplayDryRun(replayRequest)

			assert.NotNil(t, err)
		})

		t.Run("should fail if unable to resolve jobs using dependency resolver", func(t *testing.T) {
			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(models.JobSpec{}, errors.New("error while fetching dag1"))
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(models.JobSpec{}, errors.New("error while fetching dag3"))
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(models.JobSpec{}, errors.New("error while fetching dag4"))
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")

			jobSvc := job.NewService(nil, nil, nil, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, nil)
			replayRequest := &models.ReplayRequest{
				Job:     specs[spec1],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}
			_, err := jobSvc.ReplayDryRun(replayRequest)

			assert.NotNil(t, err)
			merr := err.(*multierror.Error)
			assert.Equal(t, 3, merr.Len())
		})

		t.Run("should fail if tree is cyclic", func(t *testing.T) {
			cyclicDagSpec := make([]models.JobSpec, 0)
			cyclicDag1 := models.JobSpec{Name: "dag1-deps-on-dag2", Schedule: twoAMSchedule, Task: oneDayTaskWindow}
			cyclicDag2 := models.JobSpec{Name: "dag2-deps-on-dag1", Schedule: twoAMSchedule, Task: oneDayTaskWindow}
			cyclicDag1Deps := make(map[string]models.JobSpecDependency)
			cyclicDag1Deps[cyclicDag1.Name] = models.JobSpecDependency{Job: &cyclicDag2}
			cyclicDag2Deps := make(map[string]models.JobSpecDependency)
			cyclicDag2Deps[cyclicDag2.Name] = models.JobSpecDependency{Job: &cyclicDag1}
			cyclicDag1.Dependencies = cyclicDag1Deps
			cyclicDag2.Dependencies = cyclicDag2Deps
			cyclicDagSpec = append(cyclicDagSpec, cyclicDag1, cyclicDag2)

			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(cyclicDagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, cyclicDagSpec[0], nil).Return(cyclicDagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, cyclicDagSpec[1], nil).Return(cyclicDagSpec[1], nil)
			defer depenResolver.AssertExpectations(t)

			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")

			jobSvc := job.NewService(nil, nil, nil, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, nil)
			replayRequest := &models.ReplayRequest{
				Job:     cyclicDagSpec[0],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}
			_, err := jobSvc.ReplayDryRun(replayRequest)

			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), "a cycle dependency encountered in the tree")
		})

		t.Run("resolve create replay tree for a dag with three day task window and mentioned dependencies", func(t *testing.T) {
			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, compiler, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, nil)
			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")
			replayRequest := &models.ReplayRequest{
				Job:     specs[spec1],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}

			tree, err := jobSvc.ReplayDryRun(replayRequest)

			assert.Nil(t, err)
			countMap := make(map[string][]time.Time)
			getRuns(tree, countMap)
			expectedRunMap := map[string][]time.Time{}
			expectedRunMap[spec1] = []time.Time{
				time.Date(2020, time.Month(8), 5, 2, 0, 0, 0, time.UTC),
				time.Date(2020, time.Month(8), 6, 2, 0, 0, 0, time.UTC),
				time.Date(2020, time.Month(8), 7, 2, 0, 0, 0, time.UTC),
			}
			expectedRunMap[spec2] = expectedRunMap[spec1]
			expectedRunMap[spec2] = append(expectedRunMap[spec2], time.Date(2020, time.Month(8), 8, 2, 0, 0, 0, time.UTC), time.Date(2020, time.Month(8), 9, 2, 0, 0, 0, time.UTC))
			expectedRunMap[spec3] = expectedRunMap[spec2]
			expectedRunMap[spec3] = append(expectedRunMap[spec3], time.Date(2020, time.Month(8), 10, 2, 0, 0, 0, time.UTC), time.Date(2020, time.Month(8), 11, 2, 0, 0, 0, time.UTC))
			for k, v := range countMap {
				assert.Equal(t, expectedRunMap[k], v)
			}
		})

		t.Run("resolve create replay tree for a dag with three day task window and mentioned dependencies", func(t *testing.T) {
			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, compiler, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, nil)
			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayRequest := &models.ReplayRequest{
				Job:     specs[spec4],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}

			tree, err := jobSvc.ReplayDryRun(replayRequest)

			assert.Nil(t, err)
			countMap := make(map[string][]time.Time)
			getRuns(tree, countMap)
			expectedRunMap := map[string][]time.Time{}
			expectedRunMap[spec4] = []time.Time{}
			for i := 0; i <= 23; i++ {
				expectedRunMap[spec4] = append(expectedRunMap[spec4], time.Date(2020, time.Month(8), 5, i, 0, 0, 0, time.UTC))
			}
			expectedRunMap[spec5] = []time.Time{
				time.Date(2020, time.Month(8), 5, 0, 0, 0, 0, time.UTC),
				time.Date(2020, time.Month(8), 6, 0, 0, 0, 0, time.UTC),
				time.Date(2020, time.Month(8), 7, 0, 0, 0, 0, time.UTC),
				time.Date(2020, time.Month(8), 8, 0, 0, 0, 0, time.UTC),
			}
			expectedRunMap[spec6] = append(expectedRunMap[spec5], time.Date(2020, time.Month(8), 9, 0, 0, 0, 0, time.UTC), time.Date(2020, time.Month(8), 10, 0, 0, 0, 0, time.UTC))
			for k, v := range countMap {
				assert.Equal(t, expectedRunMap[k], v)
			}
		})
	})

	t.Run("Replay", func(t *testing.T) {
		t.Run("should fail if unable to fetch jobSpecs from project jobSpecRepo", func(t *testing.T) {
			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(nil, errors.New("error while getting all dags"))
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")

			jobSvc := job.NewService(nil, nil, nil, dumpAssets, nil, nil, nil, projJobSpecRepoFac, nil)
			replayRequest := &models.ReplayRequest{
				Job:     specs[spec1],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}
			_, err := jobSvc.Replay(ctx, replayRequest)

			assert.NotNil(t, err)
		})

		t.Run("should fail if replay manager throws an error", func(t *testing.T) {
			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")
			replayRequest := &models.ReplayRequest{
				Job:        specs[spec1],
				Start:      replayStart,
				End:        replayEnd,
				Project:    projSpec,
				JobSpecMap: specs,
			}

			errMessage := "error with replay manager"
			replayManager := new(mock.ReplayManager)
			replayManager.On("Replay", ctx, replayRequest).Return("", errors.New(errMessage))
			defer replayManager.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, nil, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, replayManager)

			_, err := jobSvc.Replay(ctx, replayRequest)
			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), errMessage)
		})

		t.Run("should succeed if replay manager successfully processes request", func(t *testing.T) {
			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")
			replayRequest := &models.ReplayRequest{
				Job:        specs[spec1],
				Start:      replayStart,
				End:        replayEnd,
				Project:    projSpec,
				JobSpecMap: specs,
			}

			replayManager := new(mock.ReplayManager)
			objUUID := uuid.Must(uuid.NewRandom())
			replayManager.On("Replay", ctx, replayRequest).Return(objUUID.String(), nil)
			defer replayManager.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, nil, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, replayManager)

			replayUUID, err := jobSvc.Replay(ctx, replayRequest)
			assert.Nil(t, err)
			assert.Equal(t, objUUID.String(), replayUUID)
		})
	})

	t.Run("GetStatus", func(t *testing.T) {
		t.Run("should fail if unable to fetch replay spec", func(t *testing.T) {
			ctx := context.TODO()
			replayID := uuid.Must(uuid.NewRandom())

			replayManager := new(mock.ReplayManager)
			defer replayManager.AssertExpectations(t)
			errorMsg := "unable to fetch replay"
			replayManager.On("GetReplay", replayID).Return(&models.ReplaySpec{}, errors.New(errorMsg))

			jobSvc := job.NewService(nil, nil, nil, dumpAssets, nil, nil, nil, nil, replayManager)
			replayRequest := &models.ReplayRequest{
				ID:      replayID,
				Job:     specs[spec1],
				Project: projSpec,
			}
			_, err := jobSvc.GetStatus(ctx, replayRequest)

			assert.NotNil(t, err)
			assert.Equal(t, errorMsg, err.Error())
		})
		t.Run("should fail if unable to resolve jobs using dependency resolver", func(t *testing.T) {
			ctx := context.TODO()
			replayID := uuid.Must(uuid.NewRandom())

			startDate := time.Date(2020, time.Month(8), 5, 0, 0, 0, 0, time.UTC)
			endDate := time.Date(2020, time.Month(8), 7, 0, 0, 0, 0, time.UTC)
			replaySpec := &models.ReplaySpec{
				ID:        replayID,
				Job:       specs[spec1],
				StartDate: startDate,
				EndDate:   endDate,
				Status:    models.ReplayStatusReplayed,
			}

			replayManager := new(mock.ReplayManager)
			defer replayManager.AssertExpectations(t)
			replayManager.On("GetReplay", replayID).Return(replaySpec, nil)

			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(models.JobSpec{}, errors.New("error while fetching dag1"))
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(models.JobSpec{}, errors.New("error while fetching dag3"))
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(models.JobSpec{}, errors.New("error while fetching dag4"))
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, nil, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, replayManager)
			replayRequest := &models.ReplayRequest{
				ID:      replayID,
				Job:     specs[spec1],
				Project: projSpec,
			}
			_, err := jobSvc.GetStatus(ctx, replayRequest)

			assert.NotNil(t, err)
			merr := err.(*multierror.Error)
			assert.Equal(t, 3, merr.Len())
		})
		t.Run("resolve create replay tree with status for a dag with three day task window and mentioned dependencies", func(t *testing.T) {
			ctx := context.TODO()
			replayID := uuid.Must(uuid.NewRandom())

			startDate := time.Date(2020, time.Month(8), 5, 0, 0, 0, 0, time.UTC)
			endDate := time.Date(2020, time.Month(8), 7, 0, 0, 0, 0, time.UTC)
			replaySpec := &models.ReplaySpec{
				ID:        replayID,
				Job:       specs[spec1],
				StartDate: startDate,
				EndDate:   endDate,
				Status:    models.ReplayStatusReplayed,
			}

			jobStatusList := []models.JobStatus{
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 5, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 6, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 7, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 8, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 9, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 10, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 11, 2, 0, 0, 0, time.UTC),
				},
			}
			replayReq := &models.ReplayRequest{
				ID:         replayID,
				Job:        dagSpec[0],
				Start:      startDate,
				End:        endDate,
				Project:    projSpec,
				JobSpecMap: specs,
			}

			replayManager := new(mock.ReplayManager)
			defer replayManager.AssertExpectations(t)
			replayManager.On("GetReplay", replayID).Return(replaySpec, nil)
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[0].Name).Return([]models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2]}, nil)
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[1].Name).Return([]models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2]}, nil)
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[2].Name).Return([]models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2], jobStatusList[3], jobStatusList[4]}, nil)

			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, compiler, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, replayManager)
			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")
			replayRequest := &models.ReplayRequest{
				ID:      replayID,
				Job:     specs[spec1],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}

			tree, err := jobSvc.GetStatus(ctx, replayRequest)

			assert.Nil(t, err)
			countMap := make(map[string][]models.JobStatus)
			getRunsWithStatus(tree.Node, countMap)
			expectedRunMap := map[string][]models.JobStatus{}
			expectedRunMap[spec1] = []models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2]}
			expectedRunMap[spec2] = []models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2]}
			expectedRunMap[spec3] = []models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2], jobStatusList[3], jobStatusList[4]}
			for k, v := range countMap {
				assert.Equal(t, expectedRunMap[k], v)
			}
		})
		t.Run("should return error when job in replay is not found in the project", func(t *testing.T) {
			ctx := context.TODO()
			replayID := uuid.Must(uuid.NewRandom())

			startDate := time.Date(2020, time.Month(8), 5, 0, 0, 0, 0, time.UTC)
			endDate := time.Date(2020, time.Month(8), 7, 0, 0, 0, 0, time.UTC)
			invalidJob := models.JobSpec{
				Name: "invalid-job",
			}
			replaySpec := &models.ReplaySpec{
				ID:        replayID,
				Job:       invalidJob,
				StartDate: startDate,
				EndDate:   endDate,
				Status:    models.ReplayStatusReplayed,
			}

			replayManager := new(mock.ReplayManager)
			defer replayManager.AssertExpectations(t)
			replayManager.On("GetReplay", replayID).Return(replaySpec, nil)

			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, compiler, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, replayManager)
			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")
			replayRequest := &models.ReplayRequest{
				ID:      replayID,
				Job:     invalidJob,
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}

			_, err := jobSvc.GetStatus(ctx, replayRequest)

			assert.Equal(t, fmt.Sprintf("couldn't find any job with name %s", invalidJob.Name), err.Error())
		})
		t.Run("should return error when unable to get run status of a job", func(t *testing.T) {
			ctx := context.TODO()
			replayID := uuid.Must(uuid.NewRandom())

			startDate := time.Date(2020, time.Month(8), 5, 0, 0, 0, 0, time.UTC)
			endDate := time.Date(2020, time.Month(8), 7, 0, 0, 0, 0, time.UTC)
			replaySpec := &models.ReplaySpec{
				ID:        replayID,
				Job:       specs[spec1],
				StartDate: startDate,
				EndDate:   endDate,
				Status:    models.ReplayStatusReplayed,
			}

			replayReq := &models.ReplayRequest{
				ID:         replayID,
				Job:        dagSpec[0],
				Start:      startDate,
				End:        endDate,
				Project:    projSpec,
				JobSpecMap: specs,
			}

			replayManager := new(mock.ReplayManager)
			defer replayManager.AssertExpectations(t)
			replayManager.On("GetReplay", replayID).Return(replaySpec, nil)
			errorMsg := "unable to get status of a job run"
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[0].Name).Return([]models.JobStatus{}, errors.New(errorMsg))

			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, compiler, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, replayManager)
			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")
			replayRequest := &models.ReplayRequest{
				ID:      replayID,
				Job:     specs[spec1],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}

			_, err := jobSvc.GetStatus(ctx, replayRequest)

			assert.Equal(t, errorMsg, err.Error())
		})
		t.Run("should return error when unable to get run status of a dependent job", func(t *testing.T) {
			ctx := context.TODO()
			replayID := uuid.Must(uuid.NewRandom())

			startDate := time.Date(2020, time.Month(8), 5, 0, 0, 0, 0, time.UTC)
			endDate := time.Date(2020, time.Month(8), 7, 0, 0, 0, 0, time.UTC)
			replaySpec := &models.ReplaySpec{
				ID:        replayID,
				Job:       specs[spec1],
				StartDate: startDate,
				EndDate:   endDate,
				Status:    models.ReplayStatusReplayed,
			}

			jobStatusList := []models.JobStatus{
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 5, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 6, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 7, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 8, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 9, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 10, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 11, 2, 0, 0, 0, time.UTC),
				},
			}
			replayReq := &models.ReplayRequest{
				ID:         replayID,
				Job:        dagSpec[0],
				Start:      startDate,
				End:        endDate,
				Project:    projSpec,
				JobSpecMap: specs,
			}

			replayManager := new(mock.ReplayManager)
			defer replayManager.AssertExpectations(t)
			replayManager.On("GetReplay", replayID).Return(replaySpec, nil)
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[0].Name).Return([]models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2]}, nil)
			errorMsg := "unable to get status of a run"
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[1].Name).Return([]models.JobStatus{}, errors.New(errorMsg))

			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, compiler, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, replayManager)
			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")
			replayRequest := &models.ReplayRequest{
				ID:      replayID,
				Job:     specs[spec1],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}

			_, err := jobSvc.GetStatus(ctx, replayRequest)

			assert.Equal(t, errorMsg, err.Error())
		})
		t.Run("should return error when unable to get run status of a dependent's dependent job", func(t *testing.T) {
			ctx := context.TODO()
			replayID := uuid.Must(uuid.NewRandom())

			startDate := time.Date(2020, time.Month(8), 5, 0, 0, 0, 0, time.UTC)
			endDate := time.Date(2020, time.Month(8), 7, 0, 0, 0, 0, time.UTC)
			replaySpec := &models.ReplaySpec{
				ID:        replayID,
				Job:       specs[spec1],
				StartDate: startDate,
				EndDate:   endDate,
				Status:    models.ReplayStatusReplayed,
			}

			jobStatusList := []models.JobStatus{
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 5, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 6, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 7, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 8, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 9, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 10, 2, 0, 0, 0, time.UTC),
				},
				{
					State:       models.InstanceStateRunning,
					ScheduledAt: time.Date(2020, time.Month(8), 11, 2, 0, 0, 0, time.UTC),
				},
			}
			replayReq := &models.ReplayRequest{
				ID:         replayID,
				Job:        dagSpec[0],
				Start:      startDate,
				End:        endDate,
				Project:    projSpec,
				JobSpecMap: specs,
			}

			replayManager := new(mock.ReplayManager)
			defer replayManager.AssertExpectations(t)
			replayManager.On("GetReplay", replayID).Return(replaySpec, nil)
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[0].Name).Return([]models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2]}, nil)
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[1].Name).Return([]models.JobStatus{jobStatusList[0], jobStatusList[1], jobStatusList[2]}, nil)
			errorMsg := "unable to get status of a run"
			replayManager.On("GetRunStatus", ctx, replayReq, dagSpec[2].Name).Return([]models.JobStatus{}, errors.New(errorMsg))

			projectJobSpecRepo := new(mock.ProjectJobSpecRepository)
			projectJobSpecRepo.On("GetAll").Return(dagSpec, nil)
			defer projectJobSpecRepo.AssertExpectations(t)

			projJobSpecRepoFac := new(mock.ProjectJobSpecRepoFactory)
			projJobSpecRepoFac.On("New", projSpec).Return(projectJobSpecRepo)
			defer projJobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[0], nil).Return(dagSpec[0], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[1], nil).Return(dagSpec[1], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[2], nil).Return(dagSpec[2], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[3], nil).Return(dagSpec[3], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[4], nil).Return(dagSpec[4], nil)
			depenResolver.On("Resolve", projSpec, projectJobSpecRepo, dagSpec[5], nil).Return(dagSpec[5], nil)
			defer depenResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			jobSvc := job.NewService(nil, nil, compiler, dumpAssets, depenResolver, nil, nil, projJobSpecRepoFac, replayManager)
			replayStart, _ := time.Parse(job.ReplayDateFormat, "2020-08-05")
			replayEnd, _ := time.Parse(job.ReplayDateFormat, "2020-08-07")
			replayRequest := &models.ReplayRequest{
				ID:      replayID,
				Job:     specs[spec1],
				Start:   replayStart,
				End:     replayEnd,
				Project: projSpec,
			}

			_, err := jobSvc.GetStatus(ctx, replayRequest)

			assert.Equal(t, errorMsg, err.Error())
		})
	})
}
