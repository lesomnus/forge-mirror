# forge-mirror

Mirrors what a git forge holds and git does not carry — issues, pull requests,
comments, labels, milestones — from one forge to another.

`git push --mirror` already moves refs and objects. Everything else a forge
holds is its own invention, reachable only through a vendor API, and it stays
behind. This moves the rest.

Today: **GitHub → GitLab**, and GitLab → GitHub for the way back.

## Why it exists

It is the second half of a disaster-recovery mirror.

A repository mirror gives you the code on the day the upstream forge is
unreachable. It does not give you the issue that says why the code is like
that, the pull request where it was argued about, or the review that has not
been applied yet. For a team that has to keep working through the outage, that
missing half is most of the context.

There is no shortage of tools that move this **once** — every forge ships an
importer for migrating in. What is missing is something that keeps doing it, so
that the copy is current at the moment the original goes away.

## What it is not

- **Not two-way.** One side is the source of truth and the other is a copy. A
  copy that can write back is a path for a damaged copy to damage the original,
  which is the accident the whole arrangement exists to survive.
- **Not a migration.** It runs on a schedule and is expected to run again. Every
  write is idempotent.
- **Not a replacement for a repository mirror.** It carries no git objects. Pair
  it with one.

## How a copy is recognised again

Mirrored bodies carry a marker:

```html
<!-- forge-mirror-origin: github:acme/widget#123 issue -->
```

The mapping lives **in the target**, not in a database beside it. A table on
disk would be faster to read and would also be a second thing that can be lost,
restored to a different point in time, or quietly disagree with the target. A
marker cannot drift from the issue it is in: delete the issue and the mapping
goes with it, which is right. An index may be kept as a cache, but it must be
rebuildable from the target alone.

It is also what tells a **restore** apart from a mirror. After an outage the
target holds two kinds of thing: copies this program made, which carry a marker,
and work people did directly in the target while the source was down, which does
not. Only the second has to be carried back.

## Two modes

|  |  |
| --- | --- |
| `mirror` | source → target, on a schedule. Convergent: the target is made to match. |
| `restore` | target → source, once, after an outage. **Additive**: creates what is missing, never deletes, reports conflicts instead of resolving them. |

They are not the same run with the arguments swapped. The clients swap; the
policy does not. A mirror may overwrite because the source is authoritative. A
restore may not, because by then both sides have moved and the copy is not
entitled to win.

## Design

The domain types in [`forge/`](forge) are this program's own, not any vendor's.
A `github.Issue` passed around is a program that can only ever talk to GitHub.
A `Source` maps its vendor onto these types, a `Target` maps them onto its own,
and every decision in between — what changed, what to create, what to leave
alone — is written against them and tested without a network.

Both forges implement both ports, because each is a source on one day and a
target on another.

### Where the abstraction leaks

Named rather than hidden, because these are the places a second forge will hurt:

- **Number spaces.** GitHub gives issues and pull requests one (`#5` is one
  thing); GitLab gives them two. `Kind` carries the distinction.
- **Identity.** Authors have no account on the target and the program writes with
  one token, so authorship is rendered into the body instead of forged.
- **Numbering.** Targets assign their own id on create and will not take one you
  chose. This is why a mapping is needed at all.
- **Incremental cursors.** Every forge answers "changed since when" differently
  and at very different cost.
- **Rate limits.** Primary, secondary and search limits differ per forge, so the
  retry strategy belongs to the client, not to the caller.

## Tests

The decisions are tested against fakes, which is most of them: what to create,
what to rewrite, what to leave alone, and — the one the program rests on —
that a second run over unchanged input creates nothing.

What fakes cannot answer is whether a **real** forge hands back the same issue
when asked by marker. If it does not, an hourly job fills the target with
duplicates, and nobody watches a disaster-recovery copy closely enough to
notice. So there is an end-to-end test:

- [`lesomnus/forge-mirror-test`](https://github.com/lesomnus/forge-mirror-test)
  holds the input — an open pull request whose branch still exists, a merged one
  whose branch is gone, a closed issue, comments, an empty body, and a body that
  quotes a marker.
- `.github/workflows/e2e.yaml` starts a stock GitLab, seeds a token, mirrors the
  fixtures into it, checks what landed and what kind it landed as, **then does
  it again and requires that nothing was created.**

Baking a pre-seeded GitLab image was tried and abandoned. The image declares
`/etc/gitlab`, `/var/log/gitlab` and `/var/opt/gitlab` as volumes, and
`docker commit` does not capture volume contents — so the database holding the
seeded token is precisely the part that is not saved. A cold boot is about three
minutes and seeding another forty seconds, which is a fine price for one less
image, one less workflow and one less registry to depend on.

Not on every push all the same: four minutes and a few gigabytes is not free.

## Status

Early, and honest about it:

- `mirror` works end to end — GitHub source, GitLab target, incremental,
  idempotent.
- `restore` (target → source, after an outage) is designed but not written.
- The incremental cursor is not persisted yet. `since` bounds each run instead,
  which costs round trips but not correctness, because every write is
  idempotent.
- Attachments are not carried. Issue bodies referencing files on the source
  will break when the source does — the thing this is meant to survive.

## License

Apache-2.0.
