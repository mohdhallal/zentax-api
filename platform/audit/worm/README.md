# WORM export of the audit trail

The third leg of the ADR-0008 integrity triad. The first two — append-only at
the database, and a per-tenant hash chain — make the trail tamper-*evident*
while its rows exist. This one gets the evidence out of the database, on a
schedule, in a form an auditor can check without us.

* the job: `platform/audit/worm` (`Exporter`), registered by
  `bootstrap/audit_export.go` as the scheduler job `audit.worm-export`
* the ledger: `audit_export_segments` (migration `20260913000030`)
* the reader: `cmd/verify-audit-segment`

## What one segment is

One file, one tenant, one contiguous stretch of that tenant's chain:

```
{
  "segment": {
    "format": "zentax.audit.segment", "formatVersion": 1,
    "segmentId": "…", "segmentSeq": 3, "tenantId": "…",
    "exportedAt": "2026-09-13T09:00:00.000000Z",
    "range":  { "fromSeq": 41, "toSeq": 78, "entries": 38,
                "firstOccurredAt": "…", "lastOccurredAt": "…" },
    "chain":  { "startPrevHash": "…", "endHash": "…", "genesis": false },
    "previousSegment": { "segmentSeq": 2, "objectKey": "…", "toSeq": 40,
                         "endHash": "…", "digest": "sha256:…" },
    "verification": { … the recipe … },
    "entries": [ … every entry, in the exact form the hash covers … ]
  },
  "digest": "sha256:…"
}
```

Keys are `<prefix>/<tenant>/audit-segments/seg-<fromSeq>-<toSeq>.json`, zero
padded, so a plain listing of the prefix is the series in order.

The **last entry of a segment is normally the record of its own export**. Each
pass appends one `audit.exported` entry to the tenant's chain — attributed to
the derived system actor `worm.SystemActorID`, resource type `tenant` — inside
the same transaction that cuts the range, and the range is extended to include
it. So the trail says when evidence was taken, the evidence contains that
statement, and a tenant with nothing new produces nothing at all rather than an
endless series of files each recording the export of the last.

That entry carries the **whole header of the document about to be written**
(`worm.RecordDetails`): `segmentId`, `exportedAt`, `fromSeq`/`toSeq`/`entries`,
`objectKey`, the `previousDigest` it follows, and `preCutoverThroughSeq`. This
is not decoration. A segment's header is the file's own words about itself and
no entry hash covers it, so on its own it can be edited and the file re-sealed;
restating it inside an entry puts it under the chain, where changing it breaks
that entry's hash and every hash after it. `preCutoverThroughSeq` does the same
job for the cut-over exemption: the product states how far the unrecomputable
prefix reaches, so nothing later can be relabelled into it.

A range capped by `maxEntriesPerSegment` stops short of its own record, and a
later segment carries it. **There are two cases where a file's header is covered
by nothing, and they close differently:**

* **a capped or trailing range** — its record lands in the *next* segment, which
  may not have been exported yet. `verify-audit-segment` says so on every run,
  and the next export closes it;
* **a record written before 2026-09-13**, when the export record began restating
  the header at all. Those records state only the ordinal, the range and the
  object key, and **no later export will ever bind them** — the archive is
  write-once, so the fields stay the file's own words permanently.

Entries, range, links and end hash are bound in both cases; `segmentId` and
`exportedAt` are not, and an edit to those two alone would not be detected.
`-sealed` refuses either (below).

## Verifying a segment, without ZenTax and without the database

```
go run ./cmd/verify-audit-segment  /evidence/tenants/<tenant>/audit-segments
go run ./cmd/verify-audit-segment -series -genesis -strict  /evidence/…/audit-segments
```

Exit status 0 means everything asked for held. `-series` additionally requires
the files to form one unbroken series — and each segment to name the file before
it *by digest*, which is what stops an earlier segment being re-sealed, swapped
or replayed; `-genesis` requires that series to start at the beginning of the
tenant's chain, which is what makes it the *whole* trail rather than a window of
it; `-strict` refuses the cut-over exemption entirely, which any deployment with
no pre-2026-09-12 entries — every deployment created since — should be running;
`-sealed` refuses an archive in which **any** segment's header is unbound —
either of the two cases above.

`-strict` and `-sealed` are both preconditions, not free upgrades: on an
installation that has pre-cut-over rows `-strict` can never pass, and on one that
holds segments exported before 2026-09-13 `-sealed` can never pass — that second
kind of note never closes. Without `-sealed` each unbound header is a `note:` on
**stderr**, printed on every run, `-quiet` included (the documented automation
form is `… -quiet && echo ok`, where silence would read as "all of it is bound"),
and the run still exits 0.

**Verify the series, not a segment.** Three of the checks do not exist for a
lone file: the predecessor digests, the headers of capped segments, and whether
a pre-cut-over run is really the beginning of the tenant's chain. A file
verified alone is a file that is internally consistent; that is not the claim.

To reimplement the check in another language, read the `verification` block
inside any segment — it carries the recipe, so the file explains itself:

1. **Digest.** `sha256` of the *raw bytes of the `segment` member*, hex-encoded,
   equals the file's `digest` minus its `sha256:` prefix. Do not re-serialise
   the JSON: hash the bytes as they appear.
2. **Entry hash.** For each entry, take the field list for **its own**
   `hashVersion` — `verification.entryHashFieldsByVersion[hashVersion]`, or the
   list of the *highest* version present there when the entry states no version
   (`hashVersion` 0, "not recorded": an omitted marker must never hash less than
   the writer did) — join those fields with `"\n"`, `sha256` it, hex-encode:
   that is the entry's `hash`.
   `seq` is its decimal form; `occurredAt` is RFC 3339 in UTC with exactly six
   fractional digits; `details` is the compact, key-sorted JSON as carried in
   the file; a field the entry does not carry hashes as the empty string.

   The lists EXTEND, never reorder: version 2 is `prevHash, eventId, tenantId,
   seq, actorId, action, resourceType, resourceId, occurredAt, requestId,
   details`, and version 3 is that list plus `credentialKind, credentialId` —
   the credential the actor authenticated with, which is what lets the archive
   say *which session or token* made a change rather than only which person. One
   file may hold entries of several versions; each verifies as it was written.
3. **Links.** The first entry's `prevHash` is `chain.startPrevHash`; every later
   entry's `prevHash` is the previous entry's `hash`; the last entry's `hash` is
   `chain.endHash`.
4. **Range.** `seq` runs from `range.fromSeq` to `range.toSeq` with no gap,
   `range.entries` is the count, and `range.firstOccurredAt`/`lastOccurredAt`
   are the `occurredAt` of the first and last entries.
5. **Header.** Find the `audit.exported` entry whose `details.segmentSeq` is
   this segment's — in this file, or in a later one of the series. Nothing else
   covers those fields, and there are three shapes the record can have. Count
   how many of `segmentId`, `exportedAt`, `previousDigest` and
   `preCutoverThroughSeq` the record **states** (a record is written whole and
   its details are hash-covered, so a field that is absent was never written —
   removing one would break that entry's hash first):
   - **all four** — the range, `segmentId`, `exportedAt` and
     `previousSegment.digest` must agree with the header. A disagreement is
     tampering;
   - **none** — the record predates the sealed header (exported before
     2026-09-13). Check the **range** against it as usual, and treat the other
     four as the file's own words, bound by nothing and never to be bound. Say
     so in the report; do **not** call it an edit;
   - **some** — no writer has ever produced that shape. It is an edit that also
     rebuilt the entry hash. Refuse it.

   ⚠ **The `verification` block inside the segments does not yet carry this
   three-way rule** — its `header` step says only that the restated fields "must
   agree". A reimplementer who follows the in-file recipe literally over an
   archive containing pre-2026-09-13 records will report tampering on an
   untampered file, which is exactly the defect removed from both shipped
   readers on 2026-09-13. Until `recipe()` in `segment.go` states the rule, this
   page and the ops runbook (`zentax-ui/docs/ops/environments.md` §6.9) are the
   only places it is written down.
6. **Series.** The next segment's `chain.startPrevHash` is this one's
   `chain.endHash`, its `range.fromSeq` is this one's `range.toSeq + 1`, its
   `segmentSeq` is this one's + 1, and its `previousSegment` names **this file**:
   same `segmentId`, same `toSeq`, same `endHash`, and `digest` equal to this
   file's digest. `chain.genesis` is true only for the tenant's first segment —
   `seq` 1, `segmentSeq` 1, `startPrevHash` of 64 zeros, no `previousSegment`.

**Pre-cut-over entries.** An entry whose `hashVersion` is 1 was written before
the 2026-09-12 resolution cut-over: it hashed an instant finer than any stored
form keeps, so its hash cannot be recomputed from the row *or* from the file.
(Later versions are all recomputable — each hashes the envelope of its own
contract — and `hashVersion` 0 means "not recorded" and must be recomputed like
any other.) Such entries are link-checked only, and only inside **one contiguous
run at the very beginning of that tenant's chain**, ending at the
`preCutoverThroughSeq` the export record states.

Scoping that run to a *file* rather than to a *tenant* is worthless, and the
distinction is the whole point: every file has a top, so an attacker who
rewrites the leading entries of any recent segment and marks them pre-cut-over
never has to recompute a hash. Hence: the run must lead its segment, that
segment must be the genesis segment or follow segments that are entirely
pre-cut-over, and the sealed record must agree on where the run ends. A segment
that is not the genesis segment cannot settle this on its own and must not be
certified on its own.

What the exemption still costs, honestly: inside a tenant's genuine pre-cut-over
prefix, the entries are link-checked and no more — the same concession
`audit.Verify` makes on the rows. `-strict` refuses it, and a deployment with no
v1 rows should use it. The concession disappears for good when no v1 row is
left.

## Immutability: what you actually get

The export makes the trail *durable* and *portable*. Whether it is
*write-once* depends entirely on the destination, and the two editions differ.

**Hosted (SaaS).** The destination is a dedicated bucket with **S3 Object Lock
in compliance mode** and versioning — configured by the infrastructure, not by
this code. Under compliance mode no principal, including the account root, can
delete or overwrite an object version before its retention expires. The API's
own role needs `s3:PutObject` and nothing else; it has no delete permission and
no ability to shorten a retention period. That is the configuration in which
"WORM" is a property of the medium rather than a promise from the application.
Set it with `AUDIT_EXPORT_STORAGE_DRIVER=s3` and
`AUDIT_EXPORT_STORAGE_S3_BUCKET` / `_REGION`.

**Self-hosted (filesystem, or an object store you run).** *A filesystem cannot
enforce write-once, and this code does not pretend otherwise.* What you get
from ZenTax is:

* the segments are written and never rewritten by the application — the job has
  no delete path at all, and re-uploads only the byte-identical retry of a
  segment that has not yet landed;
* every file is self-verifying and the series is self-verifying, so a later
  alteration is **detectable** offline with `verify-audit-segment` **over the
  whole series** — entries, range, links, end hash and the predecessor digest
  are bound in every case. Over a single file it is not: the predecessor digest
  and the header record live in the neighbouring files.

  **Two exceptions, stated precisely, because "any alteration is detectable"
  is not true and was claimed here until 2026-09-13.** A segment whose header is
  unbound (the two cases in *What one segment is*) has its `segmentId` and
  `exportedAt` covered by nothing, so an edit confined to those two fields —
  re-dating an export — is not detected: one of the two closes at the next
  export, the other never. **Removal of the newest segment is likewise not
  detected** by a series run: nothing inside the archive says how far it should
  reach, so a truncated tail verifies clean. What catches both is comparing the
  archive against something outside it — the `audit_export_segments` receipts, or
  the bucket's own version listing (runbook §6.9 steps 1 and 4) — and, for the
  first, running with `-sealed`. Removal in the *middle* of a series is detected,
  by the ordinals, the sequence numbers and the predecessor digests.

What ZenTax cannot give you, and you must arrange yourself, is
**prevention**. Root on the host can edit or delete those files, and detection
after the fact is not the same control as a medium that refuses the write. If
your certification scope includes the WORM control, do at least one of:

1. point the export at an object store that enforces retention — MinIO with
   object locking, or any S3-compatible store with compliance-mode retention —
   using `AUDIT_EXPORT_STORAGE_DRIVER=s3` with `..._S3_ENDPOINT`;
2. replicate the directory, as it is written, to append-only or offline media
   (a WORM tape, a locked bucket at a provider, an immutable backup snapshot),
   and record the digests you shipped;
3. at minimum, put the export directory on a separate volume, owned by a
   different user from the ZenTax service account, mounted read-only into any
   container that does not write it, and back it up on a schedule.

And in every case: **verify what you keep**. A segment nobody ever checked is a
file, not evidence. A cron job costs milliseconds and is the difference between
holding the control and believing you do:

```
verify-audit-segment -series -genesis -quiet <dir>          # every installation
verify-audit-segment -series -genesis -strict -sealed -quiet <dir>   # see below
```

Add `-strict` only if the installation has no entries older than the 2026-09-12
hash-format cut-over, and `-sealed` only if it holds no segment exported before
2026-09-13 — both are permanent refusals otherwise, and a cron job that can never
pass is noise that gets muted. An installation that cannot use `-sealed` should
instead read the `note:` lines the run prints on stderr: they name the segments
whose `segmentId` and `exportedAt` are bound by nothing, and that list is what
belongs in the report. Neither form detects a **removed newest segment** — pair
the run with a count against the `audit_export_segments` receipts, or against the
object store's own listing.

## Operational cases the job handles

| case | what happens |
| --- | --- |
| first export for a tenant | the segment starts at `seq` 1, `chain.genesis` is true, `previousSegment` is null |
| tenant with nothing new | nothing at all: no object, no ledger row, and no audit entry — so the next pass is equally quiet |
| tenant with no entries ever | never exported; there is nothing to attest to |
| crash after the range was cut, before the object landed | the pending ledger row is found by the next pass, the bytes are rebuilt identically and uploaded under the same key |
| crash after the object landed, before the ledger was told | the same: the identical object is written again (idempotent), then confirmed |
| upload fails | the row stays pending, the attempt is counted with its reason, the cursor does not move, and the next pass retries the same range |
| destination misconfigured for a long time | the tenant stops advancing rather than skipping entries; every pass logs the failure and counts an attempt. Nothing is lost — the entries are still in the database |
| binary upgraded to a newer document format mid-flight | the pending segment is rebuilt in the new format and the ledger restated; the range it attests to cannot change (the database refuses, `ZT030`) |
| the rows behind a pending segment changed | the rebuild's digest no longer matches the one recorded at the cut; the export FAILS for that tenant and says so, rather than uploading the altered version |
| a huge backlog | exported as a series, `maxEntriesPerSegment` per file and `maxSegmentsPerRun` files per tenant per pass |

## Configuration

| setting | env | default |
| --- | --- | --- |
| `auditExport.enabled` | `AUDIT_EXPORT_ENABLED` | on |
| `auditExport.intervalMinutes` | `AUDIT_EXPORT_INTERVAL_MINUTES` | 60 |
| `auditExport.maxEntriesPerSegment` | `AUDIT_EXPORT_MAX_ENTRIES_PER_SEGMENT` | 5000 (minimum 2) |
| `auditExport.maxSegmentsPerRun` | `AUDIT_EXPORT_MAX_SEGMENTS_PER_RUN` | 20 |
| `auditExport.keyPrefix` | `AUDIT_EXPORT_KEY_PREFIX` | `tenants` |
| `auditExport.storage.driver` | `AUDIT_EXPORT_STORAGE_DRIVER` | *empty* — the document store |
| `auditExport.storage.fs.root` | `AUDIT_EXPORT_STORAGE_FS_ROOT` | `./var/audit-segments` |
| `auditExport.storage.s3.bucket` / `.region` / `.endpoint` / `.forcePathStyle` | `AUDIT_EXPORT_STORAGE_S3_*` | — |

The interval is the **RPO of the audit evidence**: how much of the trail a
catastrophic loss of the database would leave unproven. Hourly is cheap because
a quiet tenant costs one indexed query and writes nothing.
