import { cleanup,render,screen } from "@testing-library/react"
import { afterEach,expect,test,vi } from "vitest"
import { ErrorBoundary } from "./error-boundary"

afterEach(()=>{cleanup();vi.restoreAllMocks()})
test("keeps a recovery page available after a render error",()=>{
 vi.spyOn(console,"error").mockImplementation(()=>{})
 const onError=vi.fn()
 function Broken(): never { throw new Error("render failure") }
 render(<ErrorBoundary onError={onError}><Broken /></ErrorBoundary>)
 expect(screen.getByRole("alert")).toBeTruthy()
 expect(screen.getByRole("button",{name:"重新加载"})).toBeTruthy()
 expect(onError).toHaveBeenCalled()
})
