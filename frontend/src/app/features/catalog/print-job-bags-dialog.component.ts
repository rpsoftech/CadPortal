import { Component, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { catchError, map, of, shareReplay } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { SelectedDesign } from '../../core/catalog-selection';
import { toApiError } from '../../core/errors';
import { JobBagsRequest } from '../../core/models';
import { saveBlob } from '../../shared/download';

export interface PrintJobBagsData {
  designs: SelectedDesign[];
}

/** The request for one job bag per variation of the selected designs, in one metal and stone code. */
export function jobBagsRequest(
  designs: Pick<SelectedDesign, 'id'>[],
  metal: string,
  stone: string,
  skip: boolean,
): JobBagsRequest {
  return { design_ids: designs.map((d) => d.id), metal_code: metal, stone_code: stone, skip_incomplete: skip };
}

/** The variation codes the API left out (the X-Skipped header, comma separated); empty when none. */
export function skippedCodes(header: string | null): string[] {
  return (header ?? '')
    .split(',')
    .map((c) => c.trim())
    .filter((c) => c !== '');
}

/** Prints the job bags of every variation of the selected designs into one PDF. */
@Component({
  selector: 'app-print-job-bags-dialog',
  imports: [
    ReactiveFormsModule,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatProgressBarModule,
    MatSelectModule,
    MatSlideToggleModule,
  ],
  template: `
    <h2 mat-dialog-title>
      Print job bags for {{ data.designs.length }} design{{ data.designs.length === 1 ? '' : 's' }}
    </h2>
    <form [formGroup]="form" (ngSubmit)="print()" aria-label="Job bag options">
      <mat-dialog-content class="col">
        <p class="muted">
          One job bag for every variation of the selected designs, in one PDF (at most 200 bags). Pick
          the metal and the stone code the stone weights are worked out for.
        </p>
        <div class="row">
          <mat-form-field appearance="outline" class="field">
            <mat-label>Metal</mat-label>
            <mat-select formControlName="metal">
              @for (m of metals(); track m) {
                <mat-option [value]="m">{{ m }}</mat-option>
              }
            </mat-select>
          </mat-form-field>
          <mat-form-field appearance="outline" class="field">
            <mat-label>Stone code</mat-label>
            <mat-select formControlName="stone">
              @for (s of stones(); track s) {
                <mat-option [value]="s">{{ s }}</mat-option>
              }
            </mat-select>
          </mat-form-field>
        </div>
        <mat-slide-toggle formControlName="skip">
          Skip variations with missing stone data (they are listed on a last page)
        </mat-slide-toggle>
        @if (busy()) {
          <mat-progress-bar mode="indeterminate" aria-label="Making the PDF" />
        }
        <div aria-live="polite">
          @if (done()) {
            <p class="ok">The PDF has been saved.</p>
          }
          @if (skipped().length) {
            <p class="warn">Not printed (stone data missing): {{ skipped().join(', ') }}</p>
          }
        </div>
        @if (error()) {
          <div class="error-box" role="alert">{{ error() }}</div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton type="button" mat-dialog-close>{{ done() ? 'Close' : 'Cancel' }}</button>
        <button matButton="filled" type="submit" [disabled]="form.invalid || busy()">Print</button>
      </mat-dialog-actions>
    </form>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .field {
      width: 170px;
    }
    .warn {
      padding: 8px 12px;
      border-radius: 8px;
      background: var(--mat-sys-tertiary-container);
      color: var(--mat-sys-on-tertiary-container);
    }
    .ok {
      color: var(--mat-sys-primary);
    }
    @media (max-width: 720px) {
      .field {
        width: 100%;
      }
    }
  `,
})
export class PrintJobBagsDialogComponent {
  private readonly api = inject(ApiService);
  protected readonly data = inject<PrintJobBagsData>(MAT_DIALOG_DATA);

  private readonly metals$ = this.api.metals().pipe(
    map((ms) => ms.filter((m) => m.active).map((m) => m.code)),
    catchError(() => of([] as string[])),
    shareReplay(1),
  );
  private readonly stones$ = this.api.stoneCodes().pipe(
    map((ss) => ss.filter((s) => s.active).map((s) => s.code)),
    catchError(() => of([] as string[])),
    shareReplay(1),
  );
  protected readonly metals = toSignal(this.metals$, { initialValue: [] as string[] });
  protected readonly stones = toSignal(this.stones$, { initialValue: [] as string[] });

  protected readonly form = new FormGroup({
    metal: new FormControl('', { nonNullable: true, validators: Validators.required }),
    stone: new FormControl('', { nonNullable: true, validators: Validators.required }),
    skip: new FormControl(false, { nonNullable: true }),
  });

  protected readonly busy = signal(false);
  protected readonly done = signal(false);
  protected readonly error = signal('');
  protected readonly skipped = signal<string[]>([]);

  constructor() {
    // With a single active metal / stone code there is nothing to choose.
    this.metals$.subscribe((c) => this.pickOnly(this.form.controls.metal, c));
    this.stones$.subscribe((c) => this.pickOnly(this.form.controls.stone, c));
  }

  private pickOnly(ctl: FormControl<string>, codes: string[]): void {
    if (!ctl.value && codes.length === 1) ctl.setValue(codes[0]);
  }

  protected print(): void {
    if (this.form.invalid) return;
    const f = this.form.controls;
    this.busy.set(true);
    this.done.set(false);
    this.error.set('');
    this.skipped.set([]);
    this.api.jobBagsPdf(jobBagsRequest(this.data.designs, f.metal.value, f.stone.value, f.skip.value)).subscribe({
      next: (res) => {
        saveBlob(res.body!, 'jobbags.pdf');
        this.skipped.set(skippedCodes(res.headers.get('X-Skipped')));
        this.done.set(true);
        this.busy.set(false);
      },
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}
