# AI workflow research notes

This document collects background, references, and candidate ideas for improving Herald’s AI-assisted development workflow. It is non-normative: it does not describe current practice or give instructions to agents. Follow [`CLAUDE.md`](../CLAUDE.md) and [`docs/ai-workflow.md`](./ai-workflow.md) for the current workflow. An idea becomes guidance only after it is explicitly accepted and incorporated into the relevant canonical document.

## Ideas for improvement
Ideas for how to improve the AI workflow, either as takeaways from own work, or from the trailblazers out there.
- Write out a separate "AI development manifesto" for humans and agents alike. It should contain timeless rules which hold true for AI-driven development no matter what model or workflow is used at any given time. Some candidate rules are; 
    1) Keep the instructions for the AI as lean as possible, including [a minimal set of skills](https://kirahowe.com/2026/oct/20/enjoying-software-engineering-in-the-age-of-ai) and a short CLAUDE.md.
    2) Instead, build out [deterministic guardrails](https://mayhul.com/posts/state-of-engineering-2026/), including hooks, make commands and templates.
    3) Be (very!) wary of scope creep "[sure, why not?](https://drippa.ai/writing/sure-why-not-driven-development)" development.
    4) Resist the urge to initiate underspecified tasks. "[AI simply sends teams further in the wrong direction when alignment is missing](https://drippa.ai/writing/sure-why-not-driven-development)."
    5) If the task is underspecified, or if in doubt, prototype, and do it explicitly and crudely. Rebuild and "promote" to production standards later.
- Try out "Concept" spec + seams instead of traditional specs. Concepts are like specs, only more flexible and AI-friendly as they are only partly created up front. GitHub callouts (e.g. `[!AGENTS]`) are used to enrich high-level handwritten concepts with AI findings. Seams would be about deterministically defining more "contracts" at the edges and being explicit about them, referencing them from the concept docs. The idea of concept + seams + e2e/invariant/behaviour (conformance) tests is described fully in this [article](https://chats.withcaer.com/p/engineering-with-concept-art).
- A spec expert working on the AI-driven implementation of EU health regulations mentioned that specs spanning different media types (text, diagrams, test data vectors) helped the AI translate them into better code. Experiment with visual documentation in our specs.
- Label PRs by risk or "blast radius" and allow low risk PRs to merge with human review.
- Keep PRs to exactly one explicit decision each: https://kirahowe.com/2026/oct/20/enjoying-software-engineering-in-the-age-of-ai
- Implement conformance test suite in a different programming language than the product suite (or use [depguard](https://github.com/OpenPeeDeeP/depguard) with golangci-lint), preventing implementation details from leaking into the tests

## Interesting articles
* [https://kirahowe.com/2026/oct/20/enjoying-software-engineering-in-the-age-of-ai](https://kirahowe.com/2026/oct/20/enjoying-software-engineering-in-the-age-of-ai)
* [https://www.getsphere.com/blog/tax-engine](https://www.getsphere.com/blog/tax-engine)
* [https://www.xeo360.com/blog/from-rowing-to-steering-one-year-of-ai-assisted-engineering](https://www.xeo360.com/blog/from-rowing-to-steering-one-year-of-ai-assisted-engineering)
* [https://treatwell.engineering/the-heart-is-not-flat-39ac9a8ce036](https://treatwell.engineering/the-heart-is-not-flat-39ac9a8ce036)
* [https://bencohen.substack.com/p/a-fountain-of-slop](https://bencohen.substack.com/p/a-fountain-of-slop)
* [https://engineeringmaturity.substack.com/p/software-engineering-in-the-llm-funhouse](https://engineeringmaturity.substack.com/p/software-engineering-in-the-llm-funhouse)
* [https://devolutions.net/blog/development-evolutions-at-devolutions/](https://devolutions.net/blog/development-evolutions-at-devolutions/)
* [https://blog.jacobstechtavern.com/p/the-new-core-skills](https://blog.jacobstechtavern.com/p/the-new-core-skills)
* [https://bencher.dev/learn/engineering/building-bencher-ai-agents-2026/](https://bencher.dev/learn/engineering/building-bencher-ai-agents-2026/)
* [https://www.dfilipes.dev/posts/how-software-engineering-is-changing/](https://www.dfilipes.dev/posts/how-software-engineering-is-changing/)
* [https://www.manager.dev/newsletter/an-engineering-manager-s-crazy-year](https://www.manager.dev/newsletter/an-engineering-manager-s-crazy-year)
* [https://newsletter.pragmaticengineer.com/p/how-software-engineering-is-changing](https://newsletter.pragmaticengineer.com/p/how-software-engineering-is-changing)
* [https://parentnode.substack.com/p/i-dont-miss-coding-i-missed-building](https://parentnode.substack.com/p/i-dont-miss-coding-i-missed-building)
* [https://aniravi.com/posts/great-product-engineering-in-the-ai-era/](https://aniravi.com/posts/great-product-engineering-in-the-ai-era/)
* [https://mayhul.com/posts/state-of-engineering-2026/](https://mayhul.com/posts/state-of-engineering-2026/)
* [https://leviblackstone.com/writing/a-year-of-contrasts/](https://leviblackstone.com/writing/a-year-of-contrasts/)
* [https://kmaliszewski9.github.io/ai/2026/10/21/10x-what.html](https://kmaliszewski9.github.io/ai/2026/10/21/10x-what.html)
* [https://oliinyk.dev/blog/how-software-engineering-is-changing-at-f-secure/](https://oliinyk.dev/blog/how-software-engineering-is-changing-at-f-secure/)
* [https://pauldambra.dev/2026/10/machinists-not-tailors.html](https://pauldambra.dev/2026/10/machinists-not-tailors.html)
* [https://carlyrector.substack.com/p/5-new-types-of-failures-in-ai-native](https://carlyrector.substack.com/p/5-new-types-of-failures-in-ai-native)
* [https://www.wrot.pub/1361f396f268469984f71bbe521215e7](https://www.wrot.pub/1361f396f268469984f71bbe521215e7)
* [https://cucumbersome.net/two-years-of-change-how-ai-reshaped-software-engineering-at-a-small-company](https://cucumbersome.net/two-years-of-change-how-ai-reshaped-software-engineering-at-a-small-company)
* [https://medium.com/@theengineeringcriterion/what-a-small-team-with-old-code-can-do-with-ai-and-what-it-cant-yet-23a273537fb7](https://medium.com/@theengineeringcriterion/what-a-small-team-with-old-code-can-do-with-ai-and-what-it-cant-yet-23a273537fb7)
* [https://docs.google.com/document/d/1njy8qEX57V3ExOzG4lLO1aIbvQx6m9pnX4I6c6ll13Y/edit](https://docs.google.com/document/d/1njy8qEX57V3ExOzG4lLO1aIbvQx6m9pnX4I6c6ll13Y/edit)
* [https://www.bitlog.com/2026/10/23/how-software-engineering-at-hinge-has-changed/](https://www.bitlog.com/2026/10/23/how-software-engineering-at-hinge-has-changed/)
* [https://jachymdvorak.substack.com/p/what-ai-mustnt-take-from-us](https://jachymdvorak.substack.com/p/what-ai-mustnt-take-from-us)
* [https://joshuawarren.com/blog/how-software-engineering-is-changing/](https://joshuawarren.com/blog/how-software-engineering-is-changing/)
* [https://bluemind-software.ro/en/i-am-the-bottleneck-we-are/](https://bluemind-software.ro/en/i-am-the-bottleneck-we-are/)
* [https://drippa.ai/writing/sure-why-not-driven-development](https://drippa.ai/writing/sure-why-not-driven-development)
* [https://christinacodes.dev/writing/the-work-is-changing/](https://christinacodes.dev/writing/the-work-is-changing/)
* [https://davekiss.com/blog/claude-the-new-kid-in-town/](https://davekiss.com/blog/claude-the-new-kid-in-town/)
* [https://blog.siawayforward.com/posts/ai-changing-engineering-landscape/](https://blog.siawayforward.com/posts/ai-changing-engineering-landscape/)
* [https://steve-yegge.medium.com/the-ai-vampire-eda6e4f07163](https://steve-yegge.medium.com/the-ai-vampire-eda6e4f07163)
* [https://tinybigtheory.substack.com/p/on-the-impact-of-artificial-intelligence](https://tinybigtheory.substack.com/p/on-the-impact-of-artificial-intelligence)
* [https://fawziyahcodes.com/essays/ai-dead/](https://fawziyahcodes.com/essays/ai-dead/)
* [https://theknowledgeworker.substack.com/p/the-return-of-a-software-engineer](https://theknowledgeworker.substack.com/p/the-return-of-a-software-engineer)
* [https://veszelovszki.com/a/tpe/](https://veszelovszki.com/a/tpe/)
* [https://docs.google.com/document/d/1yF7eeLRlySOxQg3YNuJTjiKZ0jzujk1OaRwESb9LZwI/edit](https://docs.google.com/document/d/1yF7eeLRlySOxQg3YNuJTjiKZ0jzujk1OaRwESb9LZwI/edit)
* [https://newsletter.pragmaticengineer.com/p/the-pragmatic-engineer-essay-challenge](https://newsletter.pragmaticengineer.com/p/the-pragmatic-engineer-essay-challenge)
* [https://newsletter.pragmaticengineer.com/p/when-ai-writes-almost-all-code-what](https://newsletter.pragmaticengineer.com/p/when-ai-writes-almost-all-code-what)
* [https://blog.pragmaticengineer.com/how-software-engineering-is-changing-an-essay-challenge/](https://blog.pragmaticengineer.com/how-software-engineering-is-changing-an-essay-challenge/)
* [https://www.linkedin.com/pulse/gradually-suddenly-from-learn-code-coding-solved-back-marcell-dietl-hx3ce](https://www.linkedin.com/pulse/gradually-suddenly-from-learn-code-coding-solved-back-marcell-dietl-hx3ce)
* [https://www.linkedin.com/pulse/world-clankers-connoisseur-duncan-macquarrie-ezk6e](https://www.linkedin.com/pulse/world-clankers-connoisseur-duncan-macquarrie-ezk6e)
* [https://x.com/maanas/status/2114720498105954689](https://x.com/maanas/status/2114720498105954689)
* [https://abdulosman.com/writing/how-we-use-ai-at-work/](https://abdulosman.com/writing/how-we-use-ai-at-work/)
* [https://extelligence.substack.com/p/how-software-is-changing](https://extelligence.substack.com/p/how-software-is-changing)