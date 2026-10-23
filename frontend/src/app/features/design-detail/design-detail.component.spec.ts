import { HttpHeaders, HttpResponse } from '@angular/common/http';
import { provideZonelessChangeDetection } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { DesignDetailComponent } from './design-detail.component';

const saved = { names: [] as string[] };

const variation = (id: number, code: string) => ({
  id,
  code,
  weights: [],
  baseline_stone_carats: '0',
  seats: [],
  media: [],
});

describe('DesignDetailComponent', () => {
  const jobBagsPdf = vi.fn();

  beforeEach(() => {
    saved.names.length = 0;
    URL.createObjectURL = vi.fn(() => 'blob:test');
    URL.revokeObjectURL = vi.fn();
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      saved.names.push(this.download);
    });
    jobBagsPdf
      .mockReset()
      .mockReturnValue(
        of(new HttpResponse({ body: new Blob(['%PDF-']), headers: new HttpHeaders() })),
      );
    const api = {
      getDesign: () =>
        of({
          id: 7,
          code: 'LR0007',
          name: 'Lotus',
          category_id: 1,
          tags: [],
          media: [],
          variations: [
            variation(1, 'LR0007-V1'),
            variation(2, 'LR0007-V2'),
            variation(3, 'LR0007-V3'),
          ],
        }),
      metals: () => of([{ code: '18KR', active: true, is_default_input: true }]),
      tags: () => of([]),
      categories: () => of([]),
      stoneCodes: () => of([{ code: '2HD', active: true, is_default_baseline: true }]),
      simulateBom: () =>
        of({ lines: [], metals: [], total_pcs: 0, total_carats: '0', stone_weight_g: '0' }),
      jobBagsPdf,
    };
    TestBed.configureTestingModule({
      providers: [
        provideZonelessChangeDetection(),
        provideRouter([]),
        { provide: ApiService, useValue: api },
        { provide: AuthService, useValue: { canEdit: () => false, isAdmin: () => false } },
      ],
    });
  });

  async function open(v?: string) {
    const fixture = TestBed.createComponent(DesignDetailComponent);
    fixture.componentRef.setInput('code', 'LR0007');
    if (v !== undefined) fixture.componentRef.setInput('v', v);
    await fixture.whenStable();
    fixture.detectChanges();
    await fixture.whenStable();
    return { fixture, el: fixture.nativeElement as HTMLElement };
  }
  const button = (el: HTMLElement, text: string) =>
    [...el.querySelectorAll('button')].find((b) => b.textContent?.includes(text))!;
  const checked = (el: HTMLElement) =>
    el
      .querySelector<HTMLElement>(
        'mat-button-toggle-group [aria-checked="true"], mat-button-toggle-group .mat-button-toggle-checked',
      )
      ?.textContent?.trim();

  it('selects the variation named by ?v= and falls back to the first one', async () => {
    expect(checked((await open('LR0007-V2')).el)).toContain('LR0007-V2');
    expect(checked((await open('NOPE')).el)).toContain('LR0007-V1');
  });

  it('prints every variation of the design with the chosen metal and stone code', async () => {
    const { el } = await open();
    button(el, 'Print all job bags').click();
    expect(jobBagsPdf).toHaveBeenCalledWith({
      items: [{ variation_id: 1 }, { variation_id: 2 }, { variation_id: 3 }],
      metal_code: '18KR',
      stone_code: '2HD',
      skip_incomplete: false,
    });
    expect(saved.names).toEqual(['jobbags-LR0007.pdf']);
  });

  it('prints only the ticked variations, in on-screen order, and is disabled with none ticked', async () => {
    const { fixture, el } = await open();
    expect(button(el, 'Print selected').disabled).toBe(true);
    const boxes = el.querySelectorAll<HTMLInputElement>('[aria-label="Variations to print"] input');
    boxes[2].click();
    boxes[0].click();
    fixture.detectChanges();
    expect(button(el, 'Print selected').disabled).toBe(false);
    button(el, 'Print selected').click();
    expect(jobBagsPdf.mock.calls[0][0].items).toEqual([{ variation_id: 1 }, { variation_id: 3 }]);
  });
});
