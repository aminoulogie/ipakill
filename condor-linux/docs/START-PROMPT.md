Paste this as your first message to Claude Code, started inside `ipakill\condor-linux`:

---

Read CLAUDE.md, PLAN.md and docs/KNOWLEDGE.md first; they hold the project facts, decisions
and safety rules. Then help me with the next steps, one at a time, waiting for my OK before
anything that writes to the tablet:

1. Check the tools: run `condor doctor` (if `condor` isn't found, build and set it up from cli/).
2. The tablet is pattern-locked. Help me back up my files from it to
   `C:\Users\pro\condor-backup` (try adb first; if blocked, guide me through MTP / microSD).
3. Guide me through a factory reset from the recovery menu. Ask me to photograph the
   boot menu and describe what it shows.
4. After the reset: walk me through enabling USB debugging, then run `condor info` and
   `condor recon`, read SUMMARY.txt, and tell me what it means for Phase 2 (kernel version,
   partitions, touch/Wi-Fi drivers, root, bootloader).
5. Update the status checklist in CLAUDE.md and commit to the condor-linux branch.
