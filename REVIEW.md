# Review notes

- Keep helpers used by platform-neutral tests in platform-neutral files, and
  compile Windows tests after changing shared test inputs or helpers.
  `engine_test.go` is Unix-only; referencing its helpers from an unconstrained
  test previously broke Windows test compilation.
