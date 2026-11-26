// A design-system primitive (shadcn-style folder): page trees list it apart.
export function Button(props: { children: unknown; onClick?: () => void }) {
  return <button onClick={props.onClick}>{props.children as never}</button>
}
