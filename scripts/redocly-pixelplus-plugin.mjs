export default function pixelplusOpenApiPlugin() {
  return {
    id: "pixelplus",
    rules: {
      oas3: {
        "non-empty-responses": () => ({
          Operation(operation, { report, location }) {
            if (operation.responses && Object.keys(operation.responses).length === 0) {
              report({
                message: "Responses Object must contain at least one response.",
                location: location.child("responses"),
              });
            }
          },
        }),
      },
    },
  };
}
