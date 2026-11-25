import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': '/src',
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    css: true,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov'],
      // Gate the files introduced by the board feature (#162): CONTRIBUTING.md
      // requires >=80% coverage for new code. Legacy UI files are not covered
      // yet and are intentionally out of scope here; add them to this list
      // together with their tests.
      include: [
        'src/lib/sdlc.ts',
        'src/lib/tasks.ts',
        'src/components/board/**/*.tsx',
        'src/pages/Board.tsx',
      ],
      exclude: ['src/test/**', '**/*.test.*'],
      thresholds: {
        lines: 80,
        branches: 80,
        functions: 80,
        statements: 80,
      },
    },
  },
})
