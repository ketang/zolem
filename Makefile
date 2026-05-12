.PHONY: help smoke shatter shatter-clean

help:
	@printf 'Targets:\n'
	@printf '  smoke          Run smoke tests\n'
	@printf '  shatter        Run full shatter scan (requires SHATTER_BIN)\n'
	@printf '  shatter-clean  Remove shatter report output and write-guard artifacts\n'

smoke:
	./scripts/smoke.sh

shatter:
	./scripts/shatter-full-scan.sh

shatter-clean:
	rm -rf shatter-report .shatter
