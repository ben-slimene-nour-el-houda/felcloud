# Test Plan

## DEFERRED TESTS

## Y4 regression coverage

The following paths are covered with SQLite and the injectable mock provisioner;
they do not contact OpenStack or gateways:

- **D2 health probe:** `TestConfigureHealthProbeMode`,
  `TestHealthProbeDefaultsToBothGatewaysAndHonorsRequirement`,
  `TestHealthProbeStartupRequiresResolvableSSHSettings`, and
  `TestLoadHealthProbeSSHConfigFromInventory`.
- **D4 route readiness:** `TestGeneratedRoutesExcludeNonReadySandboxes`.
- **D9 Neutron IP collision:** `TestProvisionRetriesNeutronFixedIPConflicts`,
  `TestProvisionConflictRetriesExhaustWithoutLeakedResources`,
  `TestProvisionSkipsIPsAlreadyPresentInNeutron`, and
  `TestConcurrentProvisioningWithNeutronConflictsUsesUniqueIPs`.
- **D18 address and lease safety:** `TestValidateSandboxIPEnforcesSubnetAndAllocatorHostRange`,
  `TestReleaseRefusesManagedSandboxAndMismatchedLeaseOwner`,
  `TestReconcileApplyCannotClaimProtectedIP`, and
  `TestReconcileApplyRejectsOutOfPoolNeutronAddress`.
- **D20 recovery/reconcile:** `TestPersistProvisioningAndResourcesRejectStateRaces`,
  `TestReaperRecoversStaleProvisioningResources`,
  `TestReconcileReportsNovaOrphanWithoutDeletingIt`,
  `TestProvisionNameLocksAreReleasedAfterUse`,
  `TestProvisionFailureReportsMeasuredPartialTimings`, and
  `TestConfigureMockFailureInjection`.

These tests validate mock/static behavior only. OpenStack resource ownership and
gateway SSH probing still require the deferred live checks below.

## DEFERRED LIVE TESTS — Y4 / Nour N1 and N3

- **N1 provision/teardown:** In an isolated OpenStack project, provision one
  sandbox and confirm its Nova server ID, Neutron port ID, fixed IP, health
  probe through both gateways, and route activation. Teardown it and confirm
  only the recorded server/port IDs are deleted, the route is removed, resources
  are verified absent, and the lease enters quarantine. Repeat with an injected
  failure at each teardown stage and verify a later process resumes at the
  failed stage.
- **N3 ownership/drift:** Create a resource with `managed_by` other than
  `control-plane`; verify API, CLI, and reaper refuse teardown and emit exactly
  one `TEARDOWN_REFUSED` event. Create a Neutron port without a DB row and
  verify reconciliation reports it as an orphan without deleting it. Exercise
  each drift category and confirm apply refuses changes to protected gateway,
  bastion, and VIP addresses and to `managed_by`, `server_id`, and `port_id`.

- **Live VRRP failover**: In an isolated staging environment, set `GATEWAY_API_BASE_URL` (default `http://localhost:8081`), `CONTROL_PLANE_TOKEN` (when enabled), `FAILOVER_PROBE_URL` to a routed HTTP service hostname, and `FAILOVER_THRESHOLD_MS` (default 3000). Run `scripts/failover-test.sh`. Confirm that the status API's `vip_holder` before and after differs, the VIP response has the matching `X-Gateway-ID`, service returned HTTP 2xx/3xx within the threshold, keepalived is restored by the EXIT trap, and the `failover` event has a measured duration.
- **Control-plane failover endpoint**: POST `{"confirm":true}` to `/api/gateways/failover` in staging. Verify the active gateway is stopped and restarted through Ansible, the VIP responds from the peer, concurrent requests receive 409, and the timeout path returns 504 while still restoring keepalived.
- **HAProxy identity header deployment**: Apply the HAProxy role to both staging gateways and verify public HTTP and HTTPS responses contain `X-Gateway-ID: gw1` or `gw2`; verify the loopback health frontend on `127.0.0.1:9000` does not expose the public header behavior.
- **Shell failover test execution**: Run `scripts/run-all-tests.sh --test keepalived-weight` only as a static priority/weight check. It does not exercise real failover; the live check remains `scripts/failover-test.sh` and is intentionally excluded from the default test suite.

## DEFERRED TESTS — Remote HAProxy rollback

- **Valid apply**: In isolated staging, POST `/api/gateways/sync`; verify both candidate files pass remote `haproxy -c`, backups exist on each gateway for the returned generation, standby reloads first, both services remain active, and the generation commits.
- **Broken config rejected**: Inject an invalid candidate in a staging-only test. Verify remote validation fails on either gateway before any live file is replaced or service reloaded.
- **Unhealthy config auto-rollback**: Deploy a candidate that causes a backend regression; verify health detection identifies the newly DOWN backend during `HEALTH_REGRESSION_WINDOW_SECONDS`, a remote rollback is staged/validated/reloaded, and an `auto` rollback event records reason and duration.
- **Manual rollback checksum**: POST `/api/gateways/rollback` with an optional `target_generation`; compare `sha256sum /etc/haproxy/haproxy.cfg` on gw1 and gw2 against the selected committed snapshot after host-specific `X-Gateway-ID` insertion, and verify real duration/event fields.
- **Operation mutex**: Hold a sync or rollback operation open, then POST failover/sync/rollback concurrently; verify the competing gateway operation receives HTTP 409.
