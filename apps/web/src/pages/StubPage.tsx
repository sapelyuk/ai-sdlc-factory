import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

interface StubPageProps {
  title: string
}

export function StubPage({ title }: StubPageProps) {
  return (
    <div className="min-h-screen bg-muted/40 p-4">
      <div className="mx-auto max-w-4xl space-y-6">
        <h1 className="text-2xl font-bold">{title}</h1>
        <Card>
          <CardHeader>
            <CardTitle>Under Development</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-muted-foreground">
              The &quot;{title}&quot; page will be available in the next iteration.
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
