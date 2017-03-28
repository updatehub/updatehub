/*
 * UpdateHub
 * Copyright (C) 2017
 * O.S. Systems Sofware LTDA: contato@ossystems.com.br
 *
 * SPDX-License-Identifier:     GPL-2.0
 */

package main

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/UpdateHub/updatehub/metadata"
	"github.com/bouk/monkey"
	"github.com/stretchr/testify/assert"
)

type testController struct {
	extraPoll               int
	updateAvailable         bool
	fetchUpdateError        error
	reportCurrentStateError error
}

var checkUpdateCases = []struct {
	name         string
	controller   *testController
	initialState State
	nextState    State
}{
	{
		"UpdateAvailable",
		&testController{updateAvailable: true},
		NewUpdateCheckState(),
		&UpdateFetchState{},
	},

	{
		"UpdateNotAvailable",
		&testController{updateAvailable: false},
		NewUpdateCheckState(),
		&IdleState{},
	},
}

func (c *testController) CheckUpdate(retries int) (*metadata.UpdateMetadata, int) {
	if c.updateAvailable {
		return &metadata.UpdateMetadata{}, c.extraPoll
	}

	return nil, c.extraPoll
}

func (c *testController) FetchUpdate(updateMetadata *metadata.UpdateMetadata, cancel <-chan bool) error {
	return c.fetchUpdateError
}

func (c *testController) ReportCurrentState() error {
	return c.reportCurrentStateError
}

func TestStateUpdateCheck(t *testing.T) {
	for _, tc := range checkUpdateCases {
		t.Run(tc.name, func(t *testing.T) {
			uh, err := newTestUpdateHub(tc.initialState)
			assert.NoError(t, err)

			uh.Controller = tc.controller

			next, _ := uh.state.Handle(uh)

			assert.IsType(t, tc.nextState, next)
		})
	}
}

func TestStateUpdateFetch(t *testing.T) {
	testCases := []struct {
		name         string
		controller   *testController
		initialState State
		nextState    State
	}{
		{
			"WithoutError",
			&testController{fetchUpdateError: nil},
			NewUpdateFetchState(&metadata.UpdateMetadata{}),
			&UpdateInstallState{},
		},

		{
			"WithError",
			&testController{fetchUpdateError: errors.New("fetch error")},
			NewUpdateFetchState(&metadata.UpdateMetadata{}),
			&UpdateFetchState{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			uh, err := newTestUpdateHub(tc.initialState)
			assert.NoError(t, err)

			uh.Controller = tc.controller

			next, _ := uh.state.Handle(uh)

			assert.IsType(t, tc.nextState, next)
		})
	}
}

func TestPollingRetries(t *testing.T) {
	uh, err := newTestUpdateHub(NewPollState())
	assert.NoError(t, err)

	// Simulate time sleep
	defer func() *monkey.PatchGuard {
		return monkey.Patch(time.Sleep, func(d time.Duration) {
		})
	}().Unpatch()

	c := &testController{
		updateAvailable: false,
		extraPoll:       -1,
	}

	uh.Controller = c
	uh.settings.PollingInterval = int(time.Second)
	uh.settings.LastPoll = int(time.Now().Unix())

	next, _ := uh.state.Handle(uh)
	assert.IsType(t, &UpdateCheckState{}, next)

	for i := 1; i < 3; i++ {
		state, _ := next.Handle(uh)
		assert.IsType(t, &IdleState{}, state)
		next, _ = state.Handle(uh)
		assert.IsType(t, &PollState{}, next)
		next, _ = next.Handle(uh)
		assert.IsType(t, &UpdateCheckState{}, next)
		assert.Equal(t, i, uh.settings.PollingRetries)
	}

	c.updateAvailable = true
	c.extraPoll = 0

	next, _ = next.Handle(uh)
	assert.IsType(t, &UpdateFetchState{}, next)
	assert.Equal(t, 0, uh.settings.PollingRetries)
}

func TestPolling(t *testing.T) {
	now := time.Now()

	testCases := []struct {
		name                string
		pollingInterval     int
		firstPoll           int
		expectedElapsedTime time.Duration
	}{
		{
			"Now",
			10 * int(time.Second),
			int(now.Unix()),
			0,
		},

		{
			"NextRegularPoll",
			30 * int(time.Second),
			int(now.Add(-15 * time.Second).Unix()),
			15 * time.Second,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			uh, _ := newTestUpdateHub(nil)

			var elapsed time.Duration

			// Simulate time sleep
			defer func() *monkey.PatchGuard {
				return monkey.Patch(time.Sleep, func(d time.Duration) {
					elapsed += d
				})
			}().Unpatch()

			// Simulate time passage from now
			defer func() *monkey.PatchGuard {
				seconds := -1
				return monkey.Patch(time.Now, func() time.Time {
					seconds++
					return now.Add(time.Second * time.Duration(seconds))
				})
			}().Unpatch()

			uh.settings.PollingInterval = tc.pollingInterval
			uh.settings.FirstPoll = tc.firstPoll
			uh.settings.LastPoll = tc.firstPoll

			uh.StartPolling()

			poll := uh.state
			assert.IsType(t, &PollState{}, poll)

			poll.Handle(uh)
			assert.Equal(t, tc.expectedElapsedTime, elapsed)
		})
	}
}

func TestNewIdleState(t *testing.T) {
	state := NewIdleState()
	assert.IsType(t, &IdleState{}, state)
	assert.Equal(t, UpdateHubState(UpdateHubStateIdle), state.ID())
}

func TestStateIdle(t *testing.T) {
	testCases := []struct {
		caseName  string
		settings  *Settings
		nextState State
	}{
		{
			"PollingEnabled",
			&Settings{
				PollingSettings: PollingSettings{
					PollingEnabled: true,
				},
			},
			&PollState{},
		},

		{
			"PollingDisabled",
			&Settings{
				PollingSettings: PollingSettings{
					PollingEnabled: false,
				},
			},
			&IdleState{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.caseName, func(t *testing.T) {
			uh, err := newTestUpdateHub(NewIdleState())
			assert.NoError(t, err)

			uh.settings = tc.settings

			go func() {
				uh.state.Cancel(false)
			}()

			next, _ := uh.state.Handle(uh)
			assert.IsType(t, tc.nextState, next)
		})
	}
}

type testReportableState struct {
	BaseState
	ReportableState

	updateMetadata *metadata.UpdateMetadata
}

func (state *testReportableState) Handle(uh *UpdateHub) (State, bool) {
	return nil, true
}

func (state *testReportableState) UpdateMetadata() *metadata.UpdateMetadata {
	return state.updateMetadata
}

func TestStateUpdateInstall(t *testing.T) {
	m := &metadata.UpdateMetadata{}
	s := NewUpdateInstallState(m)

	uh, err := newTestUpdateHub(s)
	assert.NoError(t, err)

	nextState, _ := s.Handle(uh)
	expectedState := NewInstallingState(m)
	assert.Equal(t, expectedState, nextState)
}

func TestStateUpdateInstallWithChecksumError(t *testing.T) {
	expectedErr := fmt.Errorf("checksum error")

	m := &metadata.UpdateMetadata{}

	guard := monkey.PatchInstanceMethod(reflect.TypeOf(m), "Checksum", func(*metadata.UpdateMetadata) (string, error) {
		return "", expectedErr
	})
	defer guard.Unpatch()

	s := NewUpdateInstallState(m)

	uh, err := newTestUpdateHub(s)
	assert.NoError(t, err)

	nextState, _ := s.Handle(uh)
	expectedState := NewErrorState(NewTransientError(expectedErr))
	assert.Equal(t, expectedState, nextState)
}

func TestStateUpdateInstallWithUpdateMetadataAlreadyInstalled(t *testing.T) {
	m := &metadata.UpdateMetadata{}
	s := NewUpdateInstallState(m)

	uh, err := newTestUpdateHub(s)
	assert.NoError(t, err)

	uh.lastInstalledPackageUID, _ = m.Checksum()

	nextState, _ := s.Handle(uh)
	expectedState := NewWaitingForRebootState(m)
	assert.Equal(t, expectedState, nextState)
}

func TestStateInstalling(t *testing.T) {
	m := &metadata.UpdateMetadata{}
	s := NewInstallingState(m)

	uh, err := newTestUpdateHub(s)
	assert.NoError(t, err)

	nextState, _ := s.Handle(uh)
	expectedState := NewInstalledState(m)
	assert.Equal(t, expectedState, nextState)
}

func TestStateWaitingForReboot(t *testing.T) {
	m := &metadata.UpdateMetadata{}
	s := NewWaitingForRebootState(m)

	uh, err := newTestUpdateHub(s)
	assert.NoError(t, err)

	nextState, _ := s.Handle(uh)
	expectedState := NewIdleState()
	// we can't assert Equal here because NewPollState() creates a
	// channel dynamically
	assert.IsType(t, expectedState, nextState)
}

func TestStateInstalled(t *testing.T) {
	m := &metadata.UpdateMetadata{}
	s := NewInstalledState(m)

	uh, err := newTestUpdateHub(s)
	assert.NoError(t, err)

	nextState, _ := s.Handle(uh)
	expectedState := NewIdleState()
	// we can't assert Equal here because NewPollState() creates a
	// channel dynamically
	assert.IsType(t, expectedState, nextState)
}
