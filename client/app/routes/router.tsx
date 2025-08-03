import { createBrowserRouter } from "react-router-dom";
import AuthPage from "../../pages/AuthPage/ui/AuthPage";


export const router = createBrowserRouter([
    {
        path: "/",
        element: <AuthPage/>,
    },
    
])