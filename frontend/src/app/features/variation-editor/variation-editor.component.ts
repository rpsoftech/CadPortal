import { HttpEventType } from '@angular/common/http';
import { Component, computed, effect, inject, input, signal, untracked } from '@angular/core';
import { rxResource, toSignal } from '@angular/core/rxjs-interop';
import { FormArray, FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Router, RouterLink } from '@angular/router';
import { catchError, map, of } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { toApiError } from '../../core/errors';
import { FixedPipe, StoneGramsPipe, fixed } from '../../core/format';
import { BaseInput, Seat, Variation } from '../../core/models';
import { BaseInputComponent } from '../../shared/base-input.component';
import { FileDropzoneComponent } from '../../shared/file-dropzone.component';
import { positiveDecimal, positiveInt } from '../../shared/validators';

type Kind = '3dm' | 'stl';

interface UploadState {
  progress: number | null;
  error: string;
}

function seatGroup(size = '', count = '', locked = '') {
  return new FormGroup({
    size: new FormControl(size, {
      nonNullable: true,
      validators: [Validators.required, positiveDecimal(2)],
    }),
    count: new FormControl(count, {
      nonNullable: true,
      validators: [Validators.required, positiveInt],
    }),
    locked: new FormControl(locked, { nonNullable: true }),
  });
}

@Component({
  selector: 'app-variation-editor',
  imports: [
    ReactiveFormsModule,
    RouterLink,
    MatButtonModule,
    MatCardModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    MatSelectModule,
    BaseInputComponent,
    FileDropzoneComponent,
    FixedPipe,
    StoneGramsPipe,
  ],
  templateUrl: './variation-editor.component.html',
  styleUrl: './variation-editor.component.scss',
})
export class VariationEditorComponent {
  /** Route params, bound by withComponentInputBinding(). */
  readonly code = input.required<string>();
  readonly variationId = input.required<string>();

  private readonly api = inject(ApiService);
  private readonly snack = inject(MatSnackBar);
  private readonly router = inject(Router);
  protected readonly auth = inject(AuthService);

  private readonly tick = signal(0);

  protected readonly design = rxResource({
    params: () => ({ code: this.code(), tick: this.tick() }),
    stream: ({ params }) => this.api.getDesign(params.code),
  });
  protected readonly variation = computed<Variation | null>(
    () => this.design.value()?.variations.find((v) => v.id === Number(this.variationId())) ?? null,
  );
  protected readonly others = computed(
    () => this.design.value()?.variations.filter((v) => v.id !== this.variation()?.id) ?? [],
  );

  protected readonly metals = toSignal(this.api.metals().pipe(catchError(() => of([]))), {
    initialValue: [],
  });
  protected readonly stoneCodes = toSignal(
    this.api.stoneCodes().pipe(
      map((cs) => cs.filter((c) => c.active)),
      catchError(() => of([])),
    ),
    { initialValue: [] },
  );

  // ----- base input -----
  protected readonly base = signal<BaseInput | null>(null);
  protected readonly baseBusy = signal(false);
  protected readonly baseError = signal('');

  protected saveBase(): void {
    const v = this.variation();
    const base = this.base();
    if (!v || !base) return;
    this.baseBusy.set(true);
    this.baseError.set('');
    this.api.updateBaseInput(v.id, base).subscribe({
      next: () => {
        this.baseBusy.set(false);
        this.snack.open('Base input saved, weights recalculated', 'OK', { duration: 3000 });
        this.refresh();
      },
      error: async (e) => {
        this.baseError.set((await toApiError(e)).message);
        this.baseBusy.set(false);
      },
    });
  }

  // ----- CAD uploads -----
  protected readonly uploads = signal<Record<Kind, UploadState>>({
    '3dm': { progress: null, error: '' },
    stl: { progress: null, error: '' },
  });

  protected stored(kind: Kind) {
    const v = this.variation();
    return kind === '3dm'
      ? {
          name: v?.file_3dm_name ?? null,
          sha256: v?.file_3dm_sha256 ?? null,
          sizeBytes: v?.file_3dm_size_bytes ?? null,
        }
      : {
          name: v?.file_stl_name ?? null,
          sha256: v?.file_stl_sha256 ?? null,
          sizeBytes: v?.file_stl_size_bytes ?? null,
        };
  }

  private setUpload(kind: Kind, s: UploadState): void {
    this.uploads.update((u) => ({ ...u, [kind]: s }));
  }

  protected upload(kind: Kind, file: File): void {
    const v = this.variation();
    if (!v) return;
    if (!file.name.toLowerCase().endsWith(`.${kind}`)) {
      this.setUpload(kind, { progress: null, error: `Choose a .${kind} file` });
      return;
    }
    this.setUpload(kind, { progress: 0, error: '' });
    this.api.uploadCad(v.id, file).subscribe({
      next: (ev) => {
        if (ev.type === HttpEventType.UploadProgress && ev.total) {
          this.setUpload(kind, { progress: Math.round((100 * ev.loaded) / ev.total), error: '' });
        } else if (ev.type === HttpEventType.Response) {
          this.setUpload(kind, { progress: null, error: '' });
          this.snack.open(`${file.name} stored`, 'OK', { duration: 3000 });
          this.refresh();
        }
      },
      error: async (e) => {
        const err = await toApiError(e);
        this.setUpload(kind, {
          progress: null,
          error: err.status === 409 ? `Duplicate file: ${err.message}` : err.message,
        });
      },
    });
  }

  protected removeFile(kind: Kind): void {
    const v = this.variation();
    const name = this.stored(kind).name;
    if (!v || !name) return;
    if (
      !confirm(
        `Remove ${name} from ${v.code}? It is kept for a while and can be restored from Removed.`,
      )
    ) {
      return;
    }
    this.api.removeCad(v.id, kind).subscribe({
      next: () => {
        this.snack.open(`${name} removed`, 'OK', { duration: 3000 });
        this.refresh();
      },
      error: async (e) =>
        this.setUpload(kind, { progress: null, error: (await toApiError(e)).message }),
    });
  }

  // ----- stone seats (FormArray) -----
  protected readonly seatForm = new FormGroup({
    seats: new FormArray<ReturnType<typeof seatGroup>>([]),
  });
  protected get seats() {
    return this.seatForm.controls.seats;
  }

  protected readonly seatsResource = rxResource({
    params: () => ({ id: Number(this.variationId()), tick: this.tick() }),
    stream: ({ params }) => this.api.seats(params.id),
  });

  protected readonly seatBusy = signal(false);
  protected readonly seatError = signal('');
  protected readonly cloneFrom = signal<number | null>(null);

  private loadedKey = '';

  constructor() {
    // Rebuild the grid whenever fresh seats arrive from the server.
    effect(() => {
      const rows = this.seatsResource.value();
      if (!rows) return;
      untracked(() => {
        const key = JSON.stringify(
          rows.map((r: Seat) => [r.size_mm, r.stone_count, r.locked_code]),
        );
        if (key === this.loadedKey) return;
        this.loadedKey = key;
        this.seats.clear();
        for (const r of rows) {
          this.seats.push(seatGroup(fixed(r.size_mm, 2), `${r.stone_count}`, r.locked_code ?? ''));
        }
        this.seatForm.markAsPristine();
      });
    });
  }

  private readonly seatValue = toSignal(this.seatForm.valueChanges, {
    initialValue: this.seatForm.value,
  });
  protected readonly totalPcs = computed(() => {
    this.seatValue();
    return this.seats.controls.reduce((n, g) => n + (Number(g.controls.count.value) || 0), 0);
  });

  protected addSeat(): void {
    this.seats.push(seatGroup());
    this.seatForm.markAsDirty();
  }

  protected removeSeat(i: number): void {
    this.seats.removeAt(i);
    this.seatForm.markAsDirty();
  }

  protected saveSeats(): void {
    const v = this.variation();
    if (!v || this.seatForm.invalid) return;
    this.seatBusy.set(true);
    this.seatError.set('');
    const seats = this.seats.controls.map((g) => ({
      size_mm: g.controls.size.value.trim(),
      stone_count: Number(g.controls.count.value),
      locked_stone_code: g.controls.locked.value,
    }));
    this.api.setSeats(v.id, seats).subscribe({
      next: () => {
        this.seatBusy.set(false);
        this.loadedKey = ''; // force a rebuild from the server's version
        this.snack.open('Seats saved, weights recalculated', 'OK', { duration: 3000 });
        this.refresh();
      },
      error: async (e) => {
        this.seatError.set((await toApiError(e)).message);
        this.seatBusy.set(false);
      },
    });
  }

  protected cloneSeats(): void {
    const v = this.variation();
    const src = this.cloneFrom();
    if (!v || src === null) return;
    if (this.seatForm.dirty && !confirm('Replace the unsaved seat rows with the copied seats?'))
      return;
    this.seatBusy.set(true);
    this.seatError.set('');
    this.api.cloneSeats(v.id, src).subscribe({
      next: () => {
        this.seatBusy.set(false);
        this.loadedKey = '';
        this.snack.open('Seats copied', 'OK', { duration: 3000 });
        this.refresh();
      },
      error: async (e) => {
        this.seatError.set((await toApiError(e)).message);
        this.seatBusy.set(false);
      },
    });
  }

  /** True when any metal's gross is unknown, i.e. the stone matrix has a gap for these seats. */
  protected hasGap(v: Variation): boolean {
    return v.weights.some((w) => w.gross_weight_g === null);
  }

  protected deleteVariation(): void {
    const v = this.variation();
    if (!v) return;
    const files = [v.file_3dm_sha256, v.file_stl_sha256].filter(Boolean).length;
    const warning =
      `Delete ${v.code}? It disappears from the design, with its seats and ${files} stored CAD file${files === 1 ? '' : 's'}. ` +
      'It can be restored from Removed for a limited time; its code stays reserved.';
    if (!confirm(warning)) return;
    this.api.deleteVariation(v.id).subscribe({
      next: () => {
        this.snack.open(`Deleted ${v.code}`, 'OK', { duration: 4000 });
        void this.router.navigate(['/designs', this.code()]);
      },
      error: async (e) =>
        this.snack.open((await toApiError(e)).message, 'Dismiss', { duration: 6000 }),
    });
  }

  protected refresh(): void {
    this.tick.update((n) => n + 1);
  }
}
