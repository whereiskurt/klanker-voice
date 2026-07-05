import { DynamoDB } from "@aws-sdk/client-dynamodb";
import { DynamoDBDocument } from "@aws-sdk/lib-dynamodb";

const dynamodbEndpoint = process.env.AUTH_DYNAMODB_ENDPOINT;
const electroEndpoint = process.env.AUTH_ELECTRO_ENDPOINT;

// Auth.js/NextAuth DynamoDB client - for session/user management
export const dynamodbClient = DynamoDBDocument.from(
  new DynamoDB({
    credentials: {
      accessKeyId: process.env.AUTH_DYNAMODB_ID!,
      secretAccessKey: process.env.AUTH_DYNAMODB_SECRET!,
    },
    region: process.env.AWS_REGION,
    ...(dynamodbEndpoint ? { endpoint: dynamodbEndpoint } : {}),
  }),
  {
    marshallOptions: {
      convertEmptyValues: true,
      removeUndefinedValues: true,
      convertClassInstanceToMap: true,
    },
  }
);

// ElectroDB client - for profile/services data
export const electroClient = DynamoDBDocument.from(
  new DynamoDB({
    credentials: {
      accessKeyId: process.env.AUTH_ELECTRO_ID!,
      secretAccessKey: process.env.AUTH_ELECTRO_SECRET!,
    },
    region: process.env.AWS_REGION,
    ...(electroEndpoint ? { endpoint: electroEndpoint } : {}),
  }),
  {
    marshallOptions: {
      convertEmptyValues: true,
      removeUndefinedValues: true,
      convertClassInstanceToMap: true,
    },
  }
);

// Quota client/table dropped for klanker-voice (D-11): the DEF CON quota
// service (run-quota-electro) is not ported. Phase 4 rebuilds usage/quota
// against the design-spec schema on top of the electro table below.

export const DYNAMODB_TABLE = process.env.AUTH_DYNAMODB_DBNAME || "kmv-auth-authjs";
export const ELECTRO_TABLE = process.env.AUTH_ELECTRO_DBNAME || "kmv-auth-electro";
