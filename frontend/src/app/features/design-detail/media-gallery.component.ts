import { HttpEventType } from '@angular/common/http';
import { Component, computed, inject, input, output, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar } from '@angular/material/snack-bar';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import {
  MAX_MEDIA_PER_TARGET,
  MEDIA_ACCEPT,
  checkMediaFile,
  formatBytes,
  slotsLeft,
} from '../../core/media';
import { MediaItem } from '../../core/models';

/** A photo or video shown large. */
@Component({
  selector: 'app-media-viewer',
  imports: [MatDialogModule, MatButtonModule],
  template: `
    <h2 mat-dialog-title class="title">{{ item.filename }}</h2>
    <mat-dialog-content class="content">
      @if (item.kind === 'image') {
        <img [src]="item.url" [alt]="item.filename" />
      } @else {
        <video [src]="item.url" controls autoplay playsinline></video>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Close</button>
    </mat-dialog-actions>
  `,
  styles: `
    .title {
      overflow-wrap: anywhere;
    }
    .content {
      display: flex;
      justify-content: center;
    }
    img,
    video {
      max-width: 100%;
      max-height: 70vh;
      object-fit: contain;
      border-radius: 8px;
    }
  `,
})
export class MediaViewerDialogComponent {
  protected readonly item = inject<MediaItem>(MAT_DIALOG_DATA);
}

interface Target {
  /** null: the whole design. */
  variationId: number | null;
  label: string;
  items: MediaItem[];
  left: number;
}

interface UploadState {
  busy: boolean;
  /** 0 to 100 for the file being sent, or null. */
  progress: number | null;
  /** The file being sent, for the label. */
  current: string;
  errors: string[];
}

const IDLE: UploadState = { busy: false, progress: null, current: '', errors: [] };

/**
 * Photos and video of a design, and of each of its variations (up to five each, photos and
 * videos together). Everyone can look; editors add, remove and choose the catalog cover. Files go
 * one at a time, and a wrong type or an oversized file is refused before anything is sent.
 */
@Component({
  selector: 'app-media-gallery',
  imports: [MatButtonModule, MatCardModule, MatDialogModule, MatProgressBarModule],
  template: `
    <mat-card appearance="outlined">
      <mat-card-header><h2 mat-card-title>Photos & video</h2></mat-card-header>
      <mat-card-content>
        @for (t of targets(); track t.variationId) {
          <section class="target" [attr.aria-label]="'Photos and video: ' + t.label">
            <div class="row head">
              <h3>{{ t.label }}</h3>
              <span class="muted">{{ t.items.length }} of {{ max }}</span>
              <span class="spacer"></span>
              @if (canEdit()) {
                <button
                  matButton
                  (click)="picker.click()"
                  [disabled]="t.left === 0 || state(t.variationId).busy"
                  [title]="t.left === 0 ? 'Remove one first: at most ' + max + ' fit here' : ''"
                  [attr.aria-label]="'Add photo or video to ' + t.label"
                >
                  Add photo or video
                </button>
                <input
                  #picker
                  type="file"
                  hidden
                  multiple
                  [accept]="accept"
                  [attr.aria-label]="'Add photos or video to ' + t.label"
                  (change)="onPick(t, picker)"
                />
              }
            </div>

            @if (state(t.variationId).busy) {
              <div class="busy" role="status">
                Sending {{ state(t.variationId).current }}…
                <mat-progress-bar
                  aria-label="Upload progress"
                  [mode]="state(t.variationId).progress === null ? 'indeterminate' : 'determinate'"
                  [value]="state(t.variationId).progress ?? 0"
                />
              </div>
            }
            @for (e of state(t.variationId).errors; track $index) {
              <div class="error-box" role="alert">{{ e }}</div>
            }

            @if (t.items.length === 0) {
              <p class="muted small">
                {{ canEdit() ? 'Nothing here yet.' : 'No photos or video.' }}
              </p>
            } @else {
              <ul class="grid">
                @for (m of t.items; track m.id) {
                  <li>
                    <button
                      class="tile"
                      (click)="open(m)"
                      [attr.aria-label]="'Open ' + m.filename"
                      [class.cover]="m.is_cover"
                    >
                      @if (m.kind === 'image') {
                        <img [src]="m.thumb_url" alt="" loading="lazy" />
                      } @else {
                        <!-- the first frame stands in for a thumbnail; only metadata is fetched -->
                        <video
                          [src]="m.url + '#t=0.1'"
                          preload="metadata"
                          muted
                          playsinline
                        ></video>
                        <span class="play" aria-hidden="true">▶</span>
                      }
                      @if (m.is_cover) {
                        <span class="badge">Cover</span>
                      }
                    </button>
                    <div class="caption small" [title]="m.filename">{{ m.filename }}</div>
                    <div class="muted small">{{ size(m) }}</div>
                    @if (canEdit()) {
                      <div class="actions">
                        @if (m.kind === 'image' && m.variation_id === null && !m.is_cover) {
                          <button matButton (click)="makeCover(m)" [attr.aria-label]="'Make ' + m.filename + ' the cover'">
                            Make cover
                          </button>
                        }
                        <button matButton (click)="remove(m)" [attr.aria-label]="'Delete ' + m.filename">
                          Delete
                        </button>
                      </div>
                    }
                  </li>
                }
              </ul>
            }
          </section>
        }
        <p class="muted small">
          JPEG, PNG or WebP photos (up to 20 MB) and MP4, MOV or WebM video (up to 300 MB). The
          cover photo of the whole design is the picture on its catalog card.
        </p>
      </mat-card-content>
    </mat-card>
  `,
  styles: `
    :host {
      display: block;
      margin: 16px 0;
    }
    .target + .target {
      margin-top: 16px;
      padding-top: 12px;
      border-top: 1px solid var(--mat-sys-outline-variant);
    }
    h3 {
      margin: 0;
      font-size: 1rem;
    }
    .head {
      gap: 12px;
      align-items: center;
    }
    .spacer {
      flex: 1;
    }
    .grid {
      list-style: none;
      margin: 8px 0 0;
      padding: 0;
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
      gap: 12px;
    }
    .tile {
      position: relative;
      display: block;
      width: 100%;
      aspect-ratio: 4 / 3;
      padding: 0;
      border: 2px solid transparent;
      border-radius: 10px;
      overflow: hidden;
      cursor: pointer;
      background: var(--mat-sys-surface-container-high);
    }
    .tile.cover {
      border-color: var(--mat-sys-primary);
    }
    .tile img,
    .tile video {
      width: 100%;
      height: 100%;
      object-fit: cover;
      display: block;
    }
    .play {
      position: absolute;
      inset: 0;
      display: flex;
      align-items: center;
      justify-content: center;
      font-size: 2rem;
      color: white;
      text-shadow: 0 0 8px black;
    }
    .badge {
      position: absolute;
      top: 6px;
      left: 6px;
      padding: 2px 8px;
      border-radius: 10px;
      font-size: 0.75rem;
      background: var(--mat-sys-primary);
      color: var(--mat-sys-on-primary);
    }
    .caption {
      margin-top: 4px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .actions button {
      min-width: 0;
      padding-inline: 8px;
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
    }
    .busy {
      margin: 8px 0;
    }
    @media (max-width: 720px) {
      .grid {
        grid-template-columns: repeat(auto-fill, minmax(128px, 1fr));
      }
    }
  `,
})
export class MediaGalleryComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);

  readonly designId = input.required<number>();
  readonly media = input.required<MediaItem[]>();
  readonly variations = input.required<{ id: number; code: string }[]>();
  readonly canEdit = input(false);
  /** Emitted after anything was added, removed or made the cover, so the page reloads. */
  readonly changed = output<void>();

  protected readonly max = MAX_MEDIA_PER_TARGET;
  protected readonly accept = MEDIA_ACCEPT;
  private readonly states = signal<Record<string, UploadState>>({});

  protected readonly targets = computed<Target[]>(() => {
    const all = this.media();
    const build = (variationId: number | null, label: string): Target => {
      const items = all.filter((m) => m.variation_id === variationId);
      return { variationId, label, items, left: slotsLeft(items.length) };
    };
    return [build(null, 'Whole design'), ...this.variations().map((v) => build(v.id, v.code))];
  });

  protected state(variationId: number | null): UploadState {
    return this.states()[String(variationId)] ?? IDLE;
  }

  private setState(variationId: number | null, s: UploadState): void {
    this.states.update((all) => ({ ...all, [String(variationId)]: s }));
  }

  protected size(m: MediaItem): string {
    const dims = m.width && m.height ? `${m.width} × ${m.height} · ` : '';
    return dims + formatBytes(m.size_bytes);
  }

  protected open(m: MediaItem): void {
    this.dialog.open(MediaViewerDialogComponent, { data: m, width: '900px', maxWidth: '95vw' });
  }

  protected onPick(t: Target, input: HTMLInputElement): void {
    const files = Array.from(input.files ?? []);
    input.value = ''; // so choosing the same file again still fires
    if (files.length === 0) return;
    const errors: string[] = [];
    const accepted: File[] = [];
    for (const f of files) {
      const c = checkMediaFile(f);
      if (!c.ok) errors.push(c.error);
      else if (accepted.length >= t.left) {
        errors.push(
          `${f.name}: no room left (at most ${this.max} per ${t.variationId === null ? 'design' : 'variation'})`,
        );
      } else accepted.push(f);
    }
    this.setState(t.variationId, { ...IDLE, errors });
    if (accepted.length) void this.sendAll(t.variationId, accepted, errors);
  }

  /** Sends the files one after another: a video is large, and the limit is checked per upload. */
  private async sendAll(
    variationId: number | null,
    files: File[],
    errors: string[],
  ): Promise<void> {
    let sent = 0;
    for (const f of files) {
      this.setState(variationId, { busy: true, progress: 0, current: f.name, errors });
      try {
        await new Promise<void>((resolve, reject) => {
          this.api.uploadMedia(this.designId(), f, variationId).subscribe({
            next: (ev) => {
              if (ev.type === HttpEventType.UploadProgress && ev.total) {
                this.setState(variationId, {
                  busy: true,
                  progress: Math.round((100 * ev.loaded) / ev.total),
                  current: f.name,
                  errors,
                });
              } else if (ev.type === HttpEventType.Response) resolve();
            },
            error: reject,
          });
        });
        sent++;
      } catch (e) {
        errors = [...errors, `${f.name}: ${(await toApiError(e)).message}`];
        if ((e as { status?: number }).status === 409) break; // full: the rest cannot fit either
      }
    }
    this.setState(variationId, { ...IDLE, errors });
    if (sent > 0) {
      this.snack.open(`Added ${sent} file${sent === 1 ? '' : 's'}`, 'OK', { duration: 3000 });
      this.changed.emit();
    }
  }

  protected makeCover(m: MediaItem): void {
    this.api.setCover(m.id).subscribe({
      next: () => {
        this.snack.open('Cover photo changed', 'OK', { duration: 3000 });
        this.changed.emit();
      },
      error: async (e) =>
        this.snack.open((await toApiError(e)).message, 'Dismiss', { duration: 6000 }),
    });
  }

  protected remove(m: MediaItem): void {
    if (!confirm(`Delete ${m.filename}? It is kept for a while and can be restored from Removed.`))
      return;
    this.api.deleteMedia(m.id).subscribe({
      next: () => {
        this.snack.open(`${m.filename} deleted`, 'OK', { duration: 3000 });
        this.changed.emit();
      },
      error: async (e) =>
        this.snack.open((await toApiError(e)).message, 'Dismiss', { duration: 6000 }),
    });
  }
}
