# Advanced storage, locking and high availability

**Goal:** establish, with evidence, when a second host may safely take over a workload. **Requires:** chapter 15; builds on chapter 9's shared-storage ownership rules. **Budget:** 10–14 sessions plus multi-node setup time. **Distribution rule:** storage comparisons run on the Ubuntu 24.04 baseline. When following the RHEL HA guide, use a separate isolated lab on a matching supported distribution, or the baseline distribution's own supported HA stack with its documentation. Do not mix commands across distributions.

**Read first:**  [libvirt locking](https://www.libvirt.org/kbase/locking.html),  [Ceph RBD/libvirt integration](https://docs.ceph.com/en/latest/rbd/libvirt/), and the quorum/fencing sections of the  [RHEL HA guide](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/configuring_and_managing_high_availability_clusters/index), applied only in the matching environment.

## 16.1 Bounded storage comparison

1. **Shared-file baseline.** Two compute nodes mount the same dedicated NFS export with matching disk paths and verified cross-host POSIX lock behavior. Be explicit about the boundary: the storage server is a separate dependency and a single point of failure here — this lab claims VM-host failover, not storage HA. Arrange the HA stack's quorum participant and a fencing path independent of the guest management network before the failover exercise.

2. **Local comparisons.** Compare directory/LVM with a separate ZFS sandbox first.

3. **Ceph comparison.** Follow the matching Ceph release's deployment guide for a disposable three-node Ceph/RBD cluster. Record MON/OSD placement, pool replication/minimum-write settings, per-node budgets and the failure domains actually available. Starting allocation per Ceph VM: 2 vCPUs, 8 GiB RAM, a 20 GiB boot disk and a separate empty 20 GiB OSD disk — a behavior lab, not vendor sizing. Check the release's OSD memory target, retain physical-host reserve, and increase the allocation if health checks cannot pass. Confirm cluster health and a disposable RBD read/write workload before attaching a VM. Three nested VMs teach behavior but cannot demonstrate survival of their shared physical host's loss.

**Check:** backend and failure-domain tables, with a clean checkpoint preserved before each storage change.

## 16.2 Disk ownership and lock managers

*Refresher — lock versus lease:* an exclusive disk lock prevents conflicting access within its lock manager’s scope. A lease is time-bounded and must be renewed, so loss of renewal needs an enforced recovery rule. Study the virtlockd and sanlock designs with this in mind.

1. Enable the QEMU lock-manager plugin and verify it — an installed, running virtlockd daemon does not establish that the plugin is enabled ([virtlockd configuration and limits](https://www.libvirt.org/kbase/locking-lockd.html)).

2. For the shared-file baseline: verify the lockd plugin on both hosts, its backing filesystem/lockspace and matching resource identity, then attempt a conflicting disposable start.

3. Know the limits: direct locks on host-local block-device nodes give no cross-host protection, and Ceph's default RBD `exclusive-lock` can cooperatively alternate between clients — it is not, by itself, a two-writer guarantee ([Ceph's explicit warning](https://docs.ceph.com/en/latest/rbd/rbd-exclusive-locks/)).

4. For RBD: write down and implement the chosen ownership/fencing authority separately; attempt the RBD conflict test only with that authority enforced, and record RBD ownership/failover as outstanding if it is not implemented.

**Check:** the second start is refused with logs identifying the lock — a result that applies to that tested filesystem/lockspace only.

## 16.3 HA reference setup

1. In the isolated HA lab, configure the supported quorum and fencing stack per its documentation. Concrete reference: Pacemaker/Corosync managing one disposable VirtualDomain resource on the shared-file backend, with competing autostart or controller actions disabled for that resource.

2. Record the exact resource agent, quorum policy, fencing device, and proof that fencing still works when the managed node's normal network is unavailable.

3. Keep three mechanisms separate: Ceph replication/quorum, VM disk ownership, and the HA manager's quorum/fencing — one does not prove the others. A libvirt lock manager is not a complete HA scheduler.

**Check:** a diagram naming which mechanism answers which question, plus an explanation of restart-HA versus planned live migration and host crash versus management-network isolation.

## 16.4 Failure injection

1. On disposable infrastructure, simulate loss of a node, then a network partition.

2. Collect evidence that the design prevents two writers *before* any replacement VM starts, covering split-brain behavior.

If the lab cannot provide a valid quorum/fencing arrangement, complete the design analysis and mark the failover practical outstanding for the full path; the analysis counts toward the reduced path.

## 16.5 Decision table

Produce a table covering node failure, storage loss, partition and operator error — including the cases where the correct response is to stop and avoid automatic restart.

**Check:** defend each row against the 16.4 evidence.

**Chapter pass:** ownership evidence and fencing behavior demonstrated or explicitly outstanding — not merely "a VM appeared elsewhere." **Recovery:** all injection on disposable infrastructure with a documented rebuild path.
