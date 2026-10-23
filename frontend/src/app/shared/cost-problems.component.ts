import { Component, input } from '@angular/core';
import { CostDesignProblem } from '../core/models';

/** Designs and variations that could not be costed, grouped by design, with what is missing. */
@Component({
  selector: 'app-cost-problems',
  template: `
    <ul class="problems">
      @for (d of problems(); track $index) {
        <li>
          <strong class="mono">{{ d.design_code || 'Unknown design' }}</strong>
          @if (d.design_name) {
            <span class="muted"> {{ d.design_name }}</span>
          }
          @if (d.message) {
            : {{ d.message }}
          }
          @if (d.variations.length) {
            <ul>
              @for (v of d.variations; track v.variation_id) {
                <li>
                  <span class="mono">{{ v.variation_code }}</span
                  >: {{ v.message }}
                </li>
              }
            </ul>
          }
        </li>
      }
    </ul>
  `,
  styles: `
    .problems {
      margin: 4px 0;
      padding-left: 20px;
    }
    ul ul {
      margin: 2px 0 4px;
      padding-left: 18px;
    }
  `,
})
export class CostProblemsComponent {
  readonly problems = input.required<CostDesignProblem[]>();
}
