import { dl, rule } from "@lintai/sdk";
export default rule("benchmark.modules")
  .version(1)
  .program(dl`
    Decl violation(Module).
    violation(Module) :- ts.module(Module).
  `)
  .report("violation")
  .message(() => "benchmark module");
