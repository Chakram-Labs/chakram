import { createContext, useContext, useEffect, ReactNode } from 'react';

interface ThemeCtx { theme: 'dark'; toggle: () => void; }
const Ctx = createContext<ThemeCtx>({ theme: 'dark', toggle: () => {} });

export function ThemeProvider({ children }: { children: ReactNode }) {
  useEffect(() => {
    document.documentElement.classList.add('dark');
    localStorage.removeItem('chakram_theme');
  }, []);

  return <Ctx.Provider value={{ theme: 'dark', toggle: () => {} }}>{children}</Ctx.Provider>;
}

export const useTheme = () => useContext(Ctx);
