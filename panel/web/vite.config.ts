import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export default defineConfig({
  plugins: [react() as any],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
})
