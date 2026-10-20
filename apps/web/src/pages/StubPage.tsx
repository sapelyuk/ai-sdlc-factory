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
            <CardTitle>В разработке</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-muted-foreground">
              Страница «{title}» будет доступна в следующей итерации.
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
