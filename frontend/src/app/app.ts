import { Component, ElementRef, inject, signal, viewChild } from '@angular/core';
import { Title } from '@angular/platform-browser';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatMenuModule } from '@angular/material/menu';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatToolbarModule } from '@angular/material/toolbar';
import { NavigationEnd, Router, RouterLink, RouterOutlet } from '@angular/router';
import { filter } from 'rxjs';
import { AuthService } from './core/auth.service';
import { IdleService } from './core/idle.service';
import { ChangePasswordDialogComponent } from './shared/change-password-dialog.component';

@Component({
  selector: 'app-root',
  imports: [RouterOutlet, RouterLink, MatToolbarModule, MatButtonModule, MatMenuModule],
  template: `
    <a class="skip-link" href="#main" (click)="skipToContent($event)">Skip to content</a>
    <header>
    <mat-toolbar>
      <a routerLink="/" class="brand" aria-label="Golden Oak Jewels LLP, home">
        <img src="brand/mark-cream.png" alt="" class="brand-mark" />
        <span>Golden Oak</span>
      </a>
      @if (auth.user(); as u) {
        <nav class="links" aria-label="Main">
          <a matButton routerLink="/orders">Orders</a>
          @if (auth.canEdit()) {
            <a matButton routerLink="/costing-sheets">Costing sheets</a>
            <a matButton routerLink="/removed">Removed</a>
          }
          @if (auth.isAdmin()) {
            <a matButton routerLink="/master-data">Master data</a>
            <a matButton routerLink="/users">Users</a>
            <a matButton routerLink="/audit">Audit</a>
            <a matButton routerLink="/backups">Backups</a>
          }
        </nav>
        <!-- On a phone the links do not fit beside the account button, so they live in a menu. -->
        <button matButton class="nav-menu" [matMenuTriggerFor]="nav">Menu</button>
        <mat-menu #nav="matMenu">
          <a mat-menu-item routerLink="/orders">Orders</a>
          @if (auth.canEdit()) {
            <a mat-menu-item routerLink="/costing-sheets">Costing sheets</a>
            <a mat-menu-item routerLink="/removed">Removed</a>
          }
          @if (auth.isAdmin()) {
            <a mat-menu-item routerLink="/master-data">Master data</a>
            <a mat-menu-item routerLink="/users">Users</a>
            <a mat-menu-item routerLink="/audit">Audit</a>
            <a mat-menu-item routerLink="/backups">Backups</a>
          }
        </mat-menu>
        <span class="spacer"></span>
        <button
          matButton="outlined"
          class="account"
          [matMenuTriggerFor]="menu"
          [attr.aria-label]="'Account menu, ' + (u.name || u.email) + ', ' + u.role"
        >
          {{ u.name || u.email }} · {{ u.role }}
        </button>
        <mat-menu #menu="matMenu">
          <div class="menu-id muted">{{ u.email }}</div>
          <button mat-menu-item (click)="changePassword()">Change password</button>
          <button mat-menu-item (click)="auth.logout()">Sign out</button>
        </mat-menu>
      }
    </mat-toolbar>
    </header>
    <main id="main" #main tabindex="-1">
      <router-outlet />
    </main>
    <!-- Announces the new page after a route change (the tab title alone is not read out). -->
    <div class="sr-only" role="status" aria-live="polite" aria-atomic="true">{{ announcement() }}</div>
  `,
  styles: `
    .brand {
      display: inline-flex;
      align-items: center;
      gap: 10px;
      letter-spacing: 0.12em;
      text-transform: uppercase;
      color: inherit;
      text-decoration: none;
      font-weight: 600;
      font-size: 1.2rem;
      margin-right: 12px;
    }
    .brand-mark {
      height: 36px;
      width: auto;
    }
    .spacer {
      flex: 1;
    }
    .links {
      display: flex;
    }
    .nav-menu {
      display: none;
    }
    .account {
      flex: 0 1 auto;
      min-width: 0;
      max-width: 55vw;
    }
    .account ::ng-deep .mdc-button__label {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    /* The smallest phones: the name would push the bar past the screen, so the brand shows only its mark. */
    @media (max-width: 400px) {
      .brand span {
        display: none;
      }
      .account {
        max-width: 48vw;
      }
    }
    @media (max-width: 1100px) {
      .links {
        display: none;
      }
      .nav-menu {
        display: inline-flex;
      }
      .brand {
        font-size: 1.05rem;
        margin-right: 0;
      }
    }
    .menu-id {
      padding: 8px 16px;
      font-size: 0.85rem;
    }
  `,
})
export class App {
  protected readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly title = inject(Title);
  private readonly main = viewChild.required<ElementRef<HTMLElement>>('main');
  protected readonly announcement = signal('');
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);
  /** Instantiated here so the idle sign-out watches every signed-in screen. */
  private readonly idle = inject(IdleService);

  constructor() {
    // After a route change, move focus to the page heading and announce the page title, so keyboard and
    // screen-reader users start at the top of the new page instead of on a link that no longer exists.
    let first = true;
    this.router.events.pipe(filter((e) => e instanceof NavigationEnd)).subscribe(() => {
      // The first page of a visit is read from the top anyway (and the sign-in field takes focus itself).
      if (first) {
        first = false;
        this.announcement.set(this.title.getTitle());
        return;
      }
      this.announcement.set('');
      setTimeout(() => {
        this.announcement.set(this.title.getTitle());
        this.focusHeading();
      });
    });
  }

  /** Focuses the first h1 of the page; some pages draw it after their data arrives, so wait for it briefly. */
  private focusHeading(): void {
    const main = this.main().nativeElement;
    const focus = (): boolean => {
      const h1 = main.querySelector<HTMLElement>('h1');
      if (!h1) return false;
      h1.setAttribute('tabindex', '-1');
      h1.focus({ preventScroll: false });
      return true;
    };
    if (focus()) return;
    main.focus({ preventScroll: true });
    const watcher = new MutationObserver(() => {
      if (document.activeElement === main && focus()) watcher.disconnect();
    });
    watcher.observe(main, { childList: true, subtree: true });
    setTimeout(() => watcher.disconnect(), 3000);
  }

  protected skipToContent(e: Event): void {
    e.preventDefault();
    this.main().nativeElement.focus();
  }

  protected changePassword(): void {
    this.dialog
      .open<ChangePasswordDialogComponent, void, boolean>(ChangePasswordDialogComponent, {
        width: '420px',
      })
      .afterClosed()
      .subscribe((ok) => {
        if (ok) this.snack.open('Password changed', 'OK', { duration: 3000 });
      });
  }
}
