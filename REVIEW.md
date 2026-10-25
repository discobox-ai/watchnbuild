# Review notes

- Keep helpers used by platform-neutral tests in platform-neutral files, and
  compile Windows tests after changing shared test inputs or helpers.
  `engine_test.go` is Unix-only; referencing its helpers from an unconstrained
  test previously broke Windows test compilation.

- Keep command cancellation under Proc’s process-group/tree shutdown logic;
  context cancellation that kills only the shell can strand its descendants.
  Standard lint conformance retains explicit noctx exceptions for this reason.
