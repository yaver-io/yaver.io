import {
  closeReservedDogfoodBrowserWindow,
  navigateReservedDogfoodBrowserWindow,
  reserveDogfoodBrowserWindow,
} from '../dogfoodBrowserHandoff';

describe('Dogfood browser handoff', () => {
  afterEach(() => {
    closeReservedDogfoodBrowserWindow();
    delete (globalThis as any).window;
  });

  it('reserves once, paints launching UI, and navigates the same surface', () => {
    const popup = {
      closed: false,
      close: jest.fn(),
      location: { href: 'about:blank' },
      document: { title: '', body: { innerHTML: '' } },
    };
    const open = jest.fn(() => popup);
    (globalThis as any).window = { open };

    expect(reserveDogfoodBrowserWindow()).toBe(true);
    expect(reserveDogfoodBrowserWindow()).toBe(true);
    expect(open).toHaveBeenCalledTimes(1);
    expect(popup.document.body.innerHTML).toContain('Launching Dogfood');
    expect(navigateReservedDogfoodBrowserWindow('https://relay.example/d/device/dev-web/')).toBe(true);
    expect(popup.location.href).toBe('https://relay.example/d/device/dev-web/');
  });

  it('closes a reserved surface instead of navigating an unsafe scheme', () => {
    const popup = {
      closed: false,
      close: jest.fn(),
      location: { href: 'about:blank' },
      document: { title: '', body: { innerHTML: '' } },
    };
    (globalThis as any).window = { open: () => popup };
    reserveDogfoodBrowserWindow();
    expect(navigateReservedDogfoodBrowserWindow('javascript:alert(1)')).toBe(false);
    expect(popup.close).toHaveBeenCalledTimes(1);
  });
});
