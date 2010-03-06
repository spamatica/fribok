package se.swedsoft.bookkeeping.data.backup;

import se.swedsoft.bookkeeping.app.SSPath;

import java.io.*;
import java.util.List;

/**
 * Date: 2006-mar-03
 * Time: 09:39:32
 */
public class SSBackupDatabase {
    private final static File iFile = new File (SSPath.get(SSPath.USER_DATA), "backup.history");

    private static SSBackupDatabase cInstance;

    /**
     *
     * @return
     */
    public static SSBackupDatabase getInstance(){
        if (cInstance == null) {
            cInstance = new SSBackupDatabase();
        }
        return cInstance;
    }

    private SSBackupData iData;

    /**
     *
     */
    private SSBackupDatabase(){
        if (iFile.exists()) {
            loadDatabase();
        }  else {
            newDatabase();
        }
    }



    /**
     * Notify that the database has been changed and need to be stored to file
     */
    public void notifyUpdated(){
        storeDatabase();
    }

    /////////////////////////////////////////////////////////////////////////////////////


    /**
     * Creates a new backupdatabase
     */
    private void newDatabase()  {
        iData = new SSBackupData();
    }

    /**
     * Loads the backupdatabase
     *
     */
    private void loadDatabase()  {
        try {
            ObjectInputStream iObjectInputStream = new ObjectInputStream(new BufferedInputStream(new FileInputStream(iFile)));

            try{
                iData = (SSBackupData)iObjectInputStream.readObject();
            } finally{
                iObjectInputStream.close();
            }
        } catch (IOException e) {
            e.printStackTrace();
        } catch (ClassNotFoundException e) {
            e.printStackTrace();
        }

    }

    /**
     * Store the backupdatabase
     *
     */
    private void storeDatabase() {
        try {
            ObjectOutputStream iObjectOutputStream = new ObjectOutputStream(new BufferedOutputStream(new FileOutputStream(iFile)));

            iObjectOutputStream.writeObject(iData);
            iObjectOutputStream.flush();
            iObjectOutputStream.close();
        } catch (IOException e) {
            e.printStackTrace();
        }
    }


    /////////////////////////////////////////////////////////////////////////////////////


    /**
     * Returns a list of all known backups
     *
     * @return list of backups
     */
    public List<SSBackup> getBackups() {
        if(iData == null){
            throw new RuntimeException("Backupdatabase not loaded");
        }

        return iData.getBackups();
    }



}
