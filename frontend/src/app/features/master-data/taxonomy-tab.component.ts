import { Component, computed, inject, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialog } from '@angular/material/dialog';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar } from '@angular/material/snack-bar';
import { ApiService } from '../../core/api.service';
import { Category, TAG_GROUPS, Tag } from '../../core/models';
import { CategoryDialogComponent, TagDialogComponent } from './master-data-dialogs';

@Component({
  selector: 'app-taxonomy-tab',
  imports: [MatButtonModule, MatChipsModule, MatProgressBarModule],
  template: `
    <section>
      <div class="row">
        <h3>Categories</h3>
        <span class="spacer"></span>
        <button matButton="filled" (click)="editCategory(null)">Add category</button>
      </div>
      <p class="muted">
        Each design has exactly one category. The code prefix (for example LR) is optional until the
        numbering scheme is decided.
      </p>
      @if (categories.isLoading()) {
        <mat-progress-bar mode="indeterminate" />
      }
      <table class="plain">
        <thead>
          <tr>
            <th>Name</th>
            <th>Code prefix</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          @for (c of categories.value() ?? []; track c.id) {
            <tr>
              <td>{{ c.name }}</td>
              <td class="mono">
                @if (c.code_prefix) {
                  {{ c.code_prefix }}
                } @else {
                  <span class="muted">not set</span>
                }
              </td>
              <td><button matButton (click)="editCategory(c)">Edit</button></td>
            </tr>
          }
        </tbody>
      </table>
    </section>

    <section>
      <div class="row">
        <h3>Tags</h3>
        <span class="spacer"></span>
        <button matButton="filled" (click)="editTag(null)">Add tag</button>
      </div>
      <p class="muted">
        A design can have any number of tags. Select a tag to rename it, move it to another group,
        or deactivate it (it then disappears from filters but stays on existing designs).
      </p>
      @for (g of groups(); track g.name) {
        <div class="group">
          <span class="muted group-name">{{ g.name }}</span>
          <mat-chip-set [attr.aria-label]="g.name + ' tags'">
            @for (t of g.tags; track t.id) {
              <mat-chip
                [class.off]="!t.active"
                (click)="editTag(t)"
                role="button"
                tabindex="0"
                (keydown.enter)="editTag(t)"
              >
                {{ t.name }}{{ t.active ? '' : ' (inactive)' }}
              </mat-chip>
            } @empty {
              <span class="muted">none</span>
            }
          </mat-chip-set>
        </div>
      }
    </section>
  `,
  styles: `
    section {
      margin: 0 0 24px;
    }
    h3 {
      margin: 0 0 4px;
    }
    .spacer {
      flex: 1;
    }
    .group {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 8px;
      margin: 8px 0;
    }
    .group-name {
      width: 90px;
    }
    mat-chip {
      cursor: pointer;
    }
    .off {
      opacity: 0.55;
    }
  `,
})
export class TaxonomyTabComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);

  private readonly tick = signal(0);
  protected readonly categories = rxResource({
    params: () => this.tick(),
    stream: () => this.api.categories(),
  });
  protected readonly tags = rxResource({
    params: () => this.tick(),
    stream: () => this.api.tags(true),
  });
  protected readonly groups = computed(() =>
    TAG_GROUPS.map((name) => ({
      name,
      tags: (this.tags.value() ?? []).filter((t) => t.tag_group === name),
    })),
  );

  private saved(message: string): void {
    this.snack.open(message, 'OK', { duration: 3000 });
    this.tick.update((n) => n + 1);
  }

  protected editCategory(c: Category | null): void {
    this.dialog
      .open<CategoryDialogComponent, Category | null, Category>(CategoryDialogComponent, {
        data: c,
        width: '420px',
      })
      .afterClosed()
      .subscribe((r) => r && this.saved(`Saved ${r.name}`));
  }

  protected editTag(t: Tag | null): void {
    this.dialog
      .open<TagDialogComponent, Tag | null, Tag>(TagDialogComponent, { data: t, width: '420px' })
      .afterClosed()
      .subscribe((r) => r && this.saved(`Saved ${r.name}`));
  }
}
