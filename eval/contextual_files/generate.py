"""
Generate 10 synthetic company documents for contextual memory testing.

Writes:
  data/file_01.txt ... data/file_10.txt   — documents to ingest
  data/validation.json                    — 25 QA pairs with ground-truth answers

Each file is a realistic-looking internal company document with filler content
(meeting notes, policy notices, etc.) that forces chunking, and embedded tracked
facts (people, projects, policies) that change across files via updates, deletions,
and additions.
"""
from __future__ import annotations
import json
from pathlib import Path

OUT_DIR = Path(__file__).parent / "data"

# ---------------------------------------------------------------------------
# Padding blocks — realistic corporate filler to make files large enough
# to stress chunking. 15 blocks; each ~150-200 words.
# ---------------------------------------------------------------------------

_P = [
    # 0 — infrastructure
    """\
Infrastructure & DevOps Update
-------------------------------
The platform engineering team completed the scheduled maintenance window last
Thursday without incident. Database replication lag has been reduced from an
average of 340 ms to under 80 ms following the index rebuild on the primary
cluster. Monitoring dashboards in Grafana have been updated to reflect the new
alerting thresholds agreed upon in the last SRE sync. All on-call rotations for
next month have been confirmed and shared via the internal calendar. The team
resolved three open P3 tickets related to stale cache entries in the CDN layer.
Engineers are reminded to keep incident post-mortems up to date in the wiki within
48 hours of resolution. The next scheduled maintenance window is the first Sunday
of next month between 02:00 and 05:00 UTC.""",

    # 1 — facilities
    """\
Facilities & Office Notice
---------------------------
The third-floor meeting rooms (Meridian A through D) will be unavailable on the
first Tuesday of each month between 08:00 and 10:00 for fire safety inspections.
Please plan bookings accordingly. The new ergonomic chair rollout for Floor 2 is
progressing — approximately 60% of desks have been upgraded. Facilities will
contact teams individually to schedule the remaining installations. The car park
barrier system has been upgraded; existing access fobs continue to work but staff
are asked to re-register them by end of month to receive the updated firmware.
The rooftop terrace will be closed for maintenance through to the end of the
month. Coffee machine servicing is scheduled every other Wednesday morning.""",

    # 2 — finance
    """\
Finance & Procurement Reminder
--------------------------------
All purchase orders above £2,500 require two levels of approval before submission
to the finance system. Please ensure line managers are copied on all requests to
avoid delays. The Q2 budget review cycle opens on the fifteenth; cost-centre owners
should prepare actuals vs. forecast reports using the updated template available
on the intranet. Expense claims for the previous quarter must be submitted by the
last working day of this month. Outstanding receipts from the Amsterdam offsite
should be sent directly to accounts-payable@meridian.internal. Finance will not
reprocess claims submitted after the deadline without written approval from the
CFO. Currency conversion for non-GBP expenses uses the mid-market rate on the
date of the transaction.""",

    # 3 — security
    """\
Security & Compliance Update
-----------------------------
All staff are reminded to complete the annual information security awareness
training module on the Learning Portal. Completion is mandatory and must be
finished by the end of the quarter. The IT Security team will begin quarterly
phishing simulations next month; these are sanctioned exercises and any clicks on
simulated phishing links will be recorded for training purposes only. Endpoint
agents have been updated to version 4.3.1 across all managed devices. If unusual
system behaviour is observed, raise a ticket with the Helpdesk immediately and do
not attempt to remediate the issue yourself. USB storage devices remain prohibited
on company hardware. All new joiners must complete the security induction within
their first two weeks.""",

    # 4 — engineering tooling
    """\
Engineering Tooling Notes
--------------------------
The migration from the legacy CI pipeline to the new GitHub Actions workflows is
now 85% complete. Remaining repositories are scheduled for migration in the next
two sprints. Engineers are encouraged to review the updated branching strategy
document on Confluence before starting new feature branches. The internal npm
registry has been upgraded to Verdaccio v5; package publishing keys need to be
rotated — see the onboarding wiki for instructions. Code review turnaround SLA
remains at 24 hours for normal PRs and 4 hours for hotfix branches. Test coverage
gates have been raised from 70% to 80% on all new services; existing services are
expected to reach the new threshold by end of Q3.""",

    # 5 — customer success
    """\
Customer Success Weekly Digest
-------------------------------
The customer success team closed 14 support tickets this week, with an average
first-response time of 2.1 hours, below the 3-hour SLA target. Two enterprise
accounts have requested early access to the upcoming reporting module; these have
been flagged to the product team for prioritisation. A root-cause analysis for
last week's authentication outage has been completed and shared with affected
customers. NPS surveys for the Q1 cohort went out on Monday — response rate is
currently at 34%, above the industry average of 28%. The renewal pipeline for Q3
stands at £1.2 M across 18 accounts. Three accounts are flagged as at-risk and
have been assigned dedicated success managers.""",

    # 6 — people & culture
    """\
People & Culture Note
----------------------
The next company-wide all-hands meeting is scheduled for the last Friday of the
month at 14:00 in the main auditorium and via the Zoom webinar link. Agenda items
should be submitted to the EA team by Wednesday. The mentoring programme intake
for H2 is now open — both mentors and mentees can register via the HR portal.
The Culture Committee is organising a charity fundraising day next quarter; details
will follow once venue logistics are confirmed. The Employee Assistance Programme
is available 24/7 for confidential support — details are on the intranet. The new
flexible bank holiday scheme takes effect from next month, allowing employees to
swap fixed bank holidays for alternative dates.""",

    # 7 — data platform
    """\
Data & Analytics Platform
--------------------------
The analytics warehouse migration to BigQuery is progressing to plan. Three of the
five legacy Redshift clusters have been decommissioned. Data freshness for the main
reporting layer has improved from T+1 to a T+4-hour cadence following the Airflow
DAG optimisations deployed last sprint. The data governance working group met on
Wednesday and agreed a new data classification policy — a draft will be circulated
for comment next week. All new datasets must include an owner field and a retention
tag before being promoted to the production catalogue. The self-service analytics
portal saw 312 unique users last month, a 40% increase quarter on quarter.""",

    # 8 — marketing
    """\
Marketing & Brand Update
-------------------------
The refreshed brand guidelines have been published on the company intranet under
Brand > Assets. All external communications and new slide decks should use the
updated colour palette and typography from this point forward. The demand generation
team launched two new paid campaigns targeting the DACH region last week. Early
performance data shows a 22% higher click-through rate compared to the previous
quarter. The new case study featuring our partnership with FinVault is live on the
website and has already been shared by several industry analysts. The PR agency
will pitch a long-read feature to two national tech publications next month.""",

    # 9 — legal
    """\
Legal & Contracts Review
-------------------------
The updated master services agreement template is now available on the Legal
SharePoint site. Sales engineers are asked to use the new template for all new
commercial engagements and to flag any customer requests for non-standard clauses
to the legal team before agreeing to changes. Three NDAs are currently pending
countersignature; legal will follow up directly with the relevant account executives.
All software licence renewals above £10,000 must go through the procurement
approval workflow — direct purchases that bypass this process will be reversed.
GDPR data processing addenda must be in place before any customer data is
transferred to third-party processors.""",

    # 10 — product design
    """\
Product Design Team Update
---------------------------
The design system update (version 3.1) has been published to Storybook. It includes
revised spacing tokens, an updated icon set with 40 new icons, and improved
accessibility annotations for all interactive components. Designers are asked to
migrate their Figma files to the new library before the end of the sprint. The
user research team completed six moderated usability sessions this fortnight; a
synthesis readout is scheduled for next Tuesday at 10:00. Prototype feedback from
the sessions has been incorporated into the revised onboarding flow, which is now
ready for developer handoff. All design files must be version-controlled in the
shared Figma workspace, not in personal drafts.""",

    # 11 — risk register
    """\
Operational Risk Register — Quarterly Review
---------------------------------------------
The risk register was reviewed by the senior leadership team on Wednesday. Two new
operational risks were added: dependency on a single cloud availability zone for
the batch processing workloads, and an elevated key-person dependency in the data
platform team. Mitigations for both have been assigned and are expected to be in
place by end of quarter. Three previously logged risks have been downgraded
following the completion of their respective remediation plans. The next review is
scheduled for Q3 and will include an external facilitation session with the
company's risk advisory partner. The risk register is maintained on Confluence and
accessible to all senior managers.""",

    # 12 — IT helpdesk
    """\
IT Helpdesk Statistics — Monthly Summary
-----------------------------------------
The IT Helpdesk handled 312 tickets this month, a 7% decrease compared to the
prior month. Password reset requests continue to account for the largest single
category at 28% of total volume. Average resolution time across all priority levels
was 4.2 hours, within the agreed SLA targets. Six tickets escalated to third-party
vendor support; of these, four have been resolved and two remain open. A new
self-service knowledge base article covering the most common VPN connection issues
has been published and is expected to reduce that ticket category by approximately
30% next month. Helpdesk hours are 08:00 to 19:00 Monday to Friday.""",

    # 13 — investor relations
    """\
Investor Relations & Communications Note
-----------------------------------------
The Q1 earnings summary has been approved by the board and will be published on the
investor portal on the fifteenth. Analysts who have registered for the earnings
call will receive dial-in details by email 48 hours in advance. The updated company
fact sheet, including revised ARR and customer count figures, is available to
internal stakeholders on the Finance SharePoint. All external commentary on company
performance must be cleared by the Communications team before publication — do not
discuss specific financial metrics with external parties without prior approval.
The board pack for Q2 is due from cost-centre owners by the last Friday of the
month.""",

    # 14 — engineering chapter
    """\
Engineering Chapter Meeting — Notes Summary
--------------------------------------------
The engineering chapter met last Thursday. Key topics included: (1) adopting
conventional commits as the standard for all repositories, with automated
enforcement via pre-commit hooks; (2) a proposal to standardise on Go 1.22 across
all backend services by end of Q2; (3) a discussion on improving PR review quality
through a lightweight RFC process for significant architectural changes. Action items
have been logged in Jira and assigned to respective owners. The next chapter meeting
will include a demo slot — engineers are encouraged to sign up to present work in
progress. All chapter notes are archived on Confluence under Engineering > Chapter.""",
]


def _pads(*indices: int) -> list[str]:
    return [_P[i] for i in indices]


# ---------------------------------------------------------------------------
# File definitions
# Files have: filler (pre), tracked content (middle), filler (post)
# Tracked facts are embedded in the middle — not always at the very top.
# ---------------------------------------------------------------------------

FILES: list[dict] = [
    {
        "filename": "file_01.txt",
        "title": "Meridian Systems — Company Overview & Team Directory",
        "date": "2025-01-15",
        "pre": _pads(6, 0),
        "content": """\
TEAM DIRECTORY — CURRENT AS OF JANUARY 2025
============================================

Engineering Department
  Alice Chen         — Engineering Manager
                       Joined: March 2021 | Reports to: CTO
                       Responsible for all backend and platform engineering squads.

  Bob Hartley        — Senior Developer
                       Joined: July 2019 | Reports to: Alice Chen
                       Primary focus: backend services and API layer.

Product Department
  Diana Ross         — Product Manager
                       Joined: November 2020 | Reports to: CPO
                       Responsible for the core product roadmap.

ACTIVE PROJECTS
===============
Project Atlas
  Type     : Internal — API Migration (legacy REST to gRPC)
  Status   : In Progress
  Lead     : Alice Chen
  Deadline : Q3 2025
  Budget   : £120,000

HR POLICIES (SUMMARY)
======================
Annual Leave
  All permanent employees are entitled to 25 days of paid annual leave per year,
  increasing to 27 days after 5 years of service. Leave must be approved by the
  direct line manager at least two weeks in advance for periods of three or more
  consecutive days.""",
        "post": _pads(2, 11),
    },
    {
        "filename": "file_02.txt",
        "title": "Engineering Weekly Digest — Week 8, February 2025",
        "date": "2025-02-21",
        "pre": _pads(4, 7),
        "content": """\
PROJECT ATLAS — STATUS UPDATE
==============================
Milestone 1 (service discovery layer) was completed on schedule and deployed to
staging on Monday without issues. The team is on track to meet the Q3 2025
deadline. Velocity over the last two sprints has averaged 48 story points, above
the target of 40.

Bob Hartley has been assigned as technical lead for the Atlas backend module,
taking ownership of the gRPC schema definitions and the migration tooling.
He will coordinate directly with the external API consumers on breaking-change
communication.""",
        "post": _pads(8, 13),
    },
    {
        "filename": "file_03.txt",
        "title": "HR Policy Update — Remote Work Guidelines v1.0",
        "date": "2025-03-01",
        "pre": _pads(1, 6),
        "content": """\
REMOTE WORK POLICY v1.0 — EFFECTIVE 1 MARCH 2025
=================================================
Following the conclusion of the hybrid working trial period, the following policy
applies to all permanent employees effective immediately:

  Minimum office attendance : 2 days per week
  Core in-office days       : Tuesday and Thursday (standard; teams may agree
                              alternatives with their manager)
  Flexible remote days      : Remaining working days may be worked from an approved
                              remote location

Employees are expected to be reachable during core hours (09:00–17:00) regardless
of location. Equipment and expenses for home office setup are covered under the
existing remote working allowance policy.""",
        "post": _pads(3, 9),
    },
    {
        "filename": "file_04.txt",
        "title": "Quarterly Performance Review Summary — Q1 2025",
        "date": "2025-04-02",
        "pre": _pads(6, 12),
        "content": """\
PROMOTIONS & ROLE CHANGES — EFFECTIVE 1 APRIL 2025
===================================================
Following the Q1 performance review cycle, the following role changes have been
approved by the Compensation Committee:

  Bob Hartley
    Previous title : Senior Developer
    New title      : Lead Engineer
    Effective date : 1 April 2025
    Notes          : Recognised for sustained technical leadership on Project Atlas
                     and mentoring contributions within the engineering chapter.""",
        "post": _pads(2, 10),
    },
    {
        "filename": "file_05.txt",
        "title": "Product Roadmap Announcement — Q2 2025",
        "date": "2025-04-28",
        "pre": _pads(8, 3),
        "content": """\
NEW PROJECT ANNOUNCEMENT — PROJECT NOVA
========================================
The CPO has approved the initiation of Project Nova, Meridian's mobile application
programme, with the following parameters:

  Project    : Nova
  Type       : New product — native mobile application (iOS and Android)
  Lead       : Diana Ross
  Budget     : £180,000
  Target     : Launch in Q1 2026
  Team size  : 6 (to be recruited over Q2–Q3)

Project Nova will deliver a companion mobile experience for Meridian's existing
platform customers. Diana Ross will act as product lead and will report progress
directly to the CPO on a fortnightly basis.""",
        "post": _pads(11, 5),
    },
    {
        "filename": "file_06.txt",
        "title": "HR Policy Update — Remote Work Guidelines v2.0",
        "date": "2025-06-01",
        "pre": _pads(9, 1),
        "content": """\
REMOTE WORK POLICY v2.0 — EFFECTIVE 1 JUNE 2025
================================================
Following a review of the hybrid working policy introduced in March 2025, and in
response to employee feedback gathered in the Q1 engagement survey, the minimum
office attendance requirement has been revised as follows:

  Previous minimum : 2 days per week  (Policy v1.0, effective March 2025)
  New minimum      : 3 days per week  (Policy v2.0, effective 1 June 2025)

Core office days are now Tuesday, Wednesday, and Thursday. Employees who were
previously attending exactly 2 days per week should update their schedules
accordingly. Line managers should confirm updated arrangements with their teams
by 15 June 2025.""",
        "post": _pads(7, 13),
    },
    {
        "filename": "file_07.txt",
        "title": "Team Announcement — Engineering Department",
        "date": "2025-07-14",
        "pre": _pads(6, 4),
        "content": """\
DEPARTURE NOTICE — ALICE CHEN
==============================
We would like to announce that Alice Chen, Engineering Manager, has left Meridian
Systems as of 11 July 2025. Alice joined in March 2021 and made significant
contributions to the platform engineering function during her tenure.

During the search for a permanent replacement, Bob Hartley will serve as acting
Engineering Manager, taking on responsibility for team oversight and line management
in addition to his existing technical duties.

We wish Alice the very best in her future endeavours.""",
        "post": _pads(0, 12),
    },
    {
        "filename": "file_08.txt",
        "title": "Project Status Review — August 2025",
        "date": "2025-08-20",
        "pre": _pads(7, 2),
        "content": """\
PROJECT ATLAS — CANCELLATION NOTICE
=====================================
Following the budget reallocation approved by the Board in July 2025, Project Atlas
has been officially cancelled, effective immediately.

  Project  : Atlas
  Status   : Cancelled
  Reason   : Budget resources have been reallocated to accelerate Project Nova
             following stronger-than-expected market demand signals.

All Atlas team members are being reassigned. Engineers currently on Atlas have been
offered positions on the Project Nova team. Outstanding Atlas deliverables will not
be completed; the legacy REST endpoints will remain in service until further notice.""",
        "post": _pads(3, 14),
    },
    {
        "filename": "file_09.txt",
        "title": "Leadership Promotions — September 2025",
        "date": "2025-09-08",
        "pre": _pads(6, 10),
        "content": """\
LEADERSHIP PROMOTIONS — EFFECTIVE 1 SEPTEMBER 2025
===================================================
The following senior promotions have been approved:

  Diana Ross
    Previous title : Product Manager
    New title      : VP of Product
    Effective date : 1 September 2025
    Notes          : In recognition of her leadership in establishing the Project Nova
                     programme and consistent delivery of product strategy.

PROJECT NOVA — BUDGET REVISION
================================
Following the acceleration of Project Nova (see August project review), the Board
has approved an increased budget:

  Previous budget : £180,000  (approved April 2025)
  Revised budget  : £250,000
  Effective date  : 1 September 2025""",
        "post": _pads(1, 8),
    },
    {
        "filename": "file_10.txt",
        "title": "Q4 2025 Planning Document — Meridian Systems",
        "date": "2025-10-01",
        "pre": _pads(5, 11),
        "content": """\
NEW HIRE — Q4 2025
===================
  Sarah Kim
    Title  : Senior Product Designer
    Start  : 6 October 2025
    Team   : Project Nova, reporting to Diana Ross

PROJECT NOVA — REVISED TIMELINE
=================================
Following a revised scoping exercise and the addition of iOS and Android parity
requirements identified during user research, the Project Nova launch date has
been updated:

  Previous target : Q1 2026
  Revised target  : Q3 2026
  Approved by     : Diana Ross (VP of Product) and the CPO

ENGINEERING LEADERSHIP — CONFIRMATION
=======================================
Following a four-month acting period, Bob Hartley's appointment as Engineering
Manager has been confirmed on a permanent basis, effective 1 October 2025.
The external search has been stood down. Bob Hartley is no longer in an acting
capacity.""",
        "post": _pads(3, 13),
    },
]

# ---------------------------------------------------------------------------
# Validation — 25 QA pairs covering 6 categories
# ---------------------------------------------------------------------------

VALIDATION = {
    "sample_id": "contextual_files",
    "description": (
        "10 synthetic Meridian Systems company documents. "
        "Tracked entities: 4 employees, 2 projects, 2 policies. "
        "Operations across files: updates, deletions, additions, conflicts."
    ),
    "qa": [
        # ── retrieval (baseline facts, single file) ────────────────────────
        {
            "question": "What is the annual leave entitlement for employees at Meridian Systems?",
            "answer": "25 days of paid annual leave per year, increasing to 27 days after 5 years of service.",
            "category": "retrieval",
            "source_files": ["file_01.txt"],
        },
        {
            "question": "Who was the original lead for Project Atlas?",
            "answer": "Alice Chen",
            "category": "retrieval",
            "source_files": ["file_01.txt"],
        },
        {
            "question": "What was Project Atlas about?",
            "answer": "Project Atlas was an API migration project, migrating from legacy REST to gRPC.",
            "category": "retrieval",
            "source_files": ["file_01.txt"],
        },
        {
            "question": "What was Bob Hartley's original job title?",
            "answer": "Senior Developer",
            "category": "retrieval",
            "source_files": ["file_01.txt"],
        },
        {
            "question": "What was the original deadline for Project Atlas?",
            "answer": "Q3 2025",
            "category": "retrieval",
            "source_files": ["file_01.txt"],
        },
        {
            "question": "When was Project Nova announced?",
            "answer": "Q2 2025 (April 2025)",
            "category": "retrieval",
            "source_files": ["file_05.txt"],
        },
        {
            "question": "What was the original budget for Project Nova when it was first announced?",
            "answer": "£180,000",
            "category": "retrieval",
            "source_files": ["file_05.txt"],
        },
        {
            "question": "What was the original target launch date for Project Nova?",
            "answer": "Q1 2026",
            "category": "retrieval",
            "source_files": ["file_05.txt"],
        },
        # ── update (fact changed across files — latest should win) ─────────
        {
            "question": "What is Bob Hartley's current job title?",
            "answer": "Engineering Manager",
            "category": "update",
            "source_files": ["file_10.txt"],
        },
        {
            "question": "What is the current minimum office attendance under the remote work policy?",
            "answer": "3 days per week",
            "category": "update",
            "source_files": ["file_06.txt"],
        },
        {
            "question": "What is Diana Ross's current job title?",
            "answer": "VP of Product",
            "category": "update",
            "source_files": ["file_09.txt"],
        },
        {
            "question": "What is the current budget for Project Nova?",
            "answer": "£250,000",
            "category": "update",
            "source_files": ["file_09.txt"],
        },
        {
            "question": "When is Project Nova now expected to launch?",
            "answer": "Q3 2026",
            "category": "update",
            "source_files": ["file_10.txt"],
        },
        {
            "question": "What title was Bob Hartley promoted to in the Q1 2025 performance review?",
            "answer": "Lead Engineer",
            "category": "update",
            "source_files": ["file_04.txt"],
        },
        # ── deletion (fact negated or removed in a later file) ─────────────
        {
            "question": "Does Alice Chen still work at Meridian Systems?",
            "answer": "No, Alice Chen left Meridian Systems in July 2025.",
            "category": "deletion",
            "source_files": ["file_07.txt"],
        },
        {
            "question": "Is Project Atlas still active?",
            "answer": "No, Project Atlas was cancelled in August 2025.",
            "category": "deletion",
            "source_files": ["file_08.txt"],
        },
        {
            "question": "What happened to Project Atlas?",
            "answer": "Project Atlas was cancelled and its budget was reallocated to Project Nova.",
            "category": "deletion",
            "source_files": ["file_08.txt"],
        },
        # ── addition (fact only appears in a later file) ───────────────────
        {
            "question": "Who is Sarah Kim and what is her role?",
            "answer": "Sarah Kim is a Senior Product Designer who joined Meridian Systems in October 2025, working on Project Nova.",
            "category": "addition",
            "source_files": ["file_10.txt"],
        },
        {
            "question": "What type of product is Project Nova?",
            "answer": "A native mobile application for iOS and Android.",
            "category": "addition",
            "source_files": ["file_05.txt"],
        },
        # ── multi_hop (requires combining facts across multiple files) ──────
        {
            "question": "Who is currently managing the engineering team at Meridian Systems?",
            "answer": "Bob Hartley, confirmed as Engineering Manager in October 2025.",
            "category": "multi_hop",
            "source_files": ["file_07.txt", "file_10.txt"],
        },
        {
            "question": "How did the remote work policy change between March 2025 and June 2025?",
            "answer": "The minimum office attendance increased from 2 days per week (Policy v1.0, March 2025) to 3 days per week (Policy v2.0, June 2025).",
            "category": "multi_hop",
            "source_files": ["file_03.txt", "file_06.txt"],
        },
        {
            "question": "Which projects is Meridian Systems currently working on?",
            "answer": "Project Nova. Project Atlas was cancelled.",
            "category": "multi_hop",
            "source_files": ["file_05.txt", "file_08.txt"],
        },
        {
            "question": "What role did Diana Ross hold before becoming VP of Product?",
            "answer": "Product Manager",
            "category": "multi_hop",
            "source_files": ["file_01.txt", "file_09.txt"],
        },
        # ── conflict (two competing facts; latest file should override) ─────
        {
            "question": "Is Bob Hartley currently in an acting or permanent Engineering Manager role?",
            "answer": "Permanent. His appointment was confirmed on 1 October 2025; he is no longer acting.",
            "category": "conflict",
            "source_files": ["file_07.txt", "file_10.txt"],
        },
        {
            "question": "Who does Sarah Kim report to?",
            "answer": "Diana Ross (VP of Product)",
            "category": "conflict",
            "source_files": ["file_10.txt"],
        },
    ],
}

# ---------------------------------------------------------------------------
# Writer
# ---------------------------------------------------------------------------

def _build_file(f: dict) -> str:
    parts = [
        "=" * 70,
        f"  {f['title']}",
        f"  Date: {f['date']}",
        "=" * 70,
        "",
    ]
    for block in f["pre"]:
        parts.append(block)
        parts.append("")
    parts.append(f["content"])
    parts.append("")
    for block in f["post"]:
        parts.append(block)
        parts.append("")
    return "\n".join(parts)


def main() -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)

    for file_def in FILES:
        path = OUT_DIR / file_def["filename"]
        path.write_text(_build_file(file_def), encoding="utf-8")
        print(f"  wrote {file_def['filename']}  ({path.stat().st_size:,} bytes)")

    val_path = OUT_DIR / "validation.json"
    val_path.write_text(json.dumps(VALIDATION, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"  wrote validation.json  ({len(VALIDATION['qa'])} QA pairs)")
    print(f"\nAll files written to: {OUT_DIR.resolve()}")


if __name__ == "__main__":
    main()
